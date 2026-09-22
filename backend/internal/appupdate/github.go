package appupdate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ghAPIClient GitHub API 专用客户端。
//
// 不能用 http.DefaultClient：它没有超时，GitHub 无响应时请求会一直挂到
// TCP 层超时，而这些调用都发生在 HTTP handler 内。
var ghAPIClient = &http.Client{Timeout: 15 * time.Second}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// stripVersionSuffix 去除版本号中的后缀（如 ~670ab34），仅保留主版本号
func stripVersionSuffix(v string) string {
	parts := strings.Split(v, "~")
	if len(parts) > 0 {
		return parts[0]
	}
	return v
}

// getLatestAlphaCoreHash 获取 Alpha 版本对应的 commit 短哈希（前7位）
func getLatestAlphaCoreHash() (string, error) {
	alphaCacheMutex.RLock()
	if latestAlphaHashCache != "" && time.Since(latestAlphaHashCacheTime) < alphaCacheTTL {
		defer alphaCacheMutex.RUnlock()
		return latestAlphaHashCache, nil
	}
	alphaCacheMutex.RUnlock()

	// 获取 Prerelease-Alpha tag 的 commit SHA
	url := "https://api.github.com/repos/MetaCubeX/mihomo/git/refs/tags/Prerelease-Alpha"
	resp, err := ghAPIClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var data struct {
		Object struct {
			Sha string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if data.Object.Sha == "" {
		return "", fmt.Errorf("no sha found")
	}

	// 取前7位作为短哈希
	hash := data.Object.Sha
	if len(hash) >= 7 {
		hash = hash[:7]
	}

	alphaCacheMutex.Lock()
	latestAlphaHashCache = hash
	latestAlphaHashCacheTime = time.Now()
	alphaCacheMutex.Unlock()

	return hash, nil
}

// getLatestVersion 从 GitHub API 获取最新 release 版本号（带缓存）。
//
// force 为 true 时无视缓存冷却直接回源（供弹窗里手动点「检查更新」使用），
// 成功后照样写回缓存并续期，使随后的自动检查从这一刻重新起算冷却。
func getLatestVersion(force bool) (string, error) {
	if !force {
		cacheMutex.RLock()
		if latestVersionCache != "" && time.Since(latestVersionCacheTime) < cacheTTL {
			cached := latestVersionCache
			cacheMutex.RUnlock()
			return cached, nil
		}
		cacheMutex.RUnlock()
	}

	url := "https://api.github.com/repos/shuangji66/fluxor/releases/latest"
	resp, err := ghAPIClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 必须回报状态码：此前误返回 err（此时为 nil），会把 403 限流 / 404 无 Release
		// 静默地当成「拿到空版本号」→ 前端显示「已是最新版本」，掩盖真实故障。
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var result struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	version := strings.TrimPrefix(result.TagName, "v")
	if version == "" {
		version = result.TagName
	}

	cacheMutex.Lock()
	latestVersionCache = version
	latestVersionCacheTime = time.Now()
	cacheMutex.Unlock()

	return version, nil
}

// compareVersions 比较两个语义化版本号
func compareVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")
	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(parts1) {
			n1, _ = strconv.Atoi(parts1[i])
		}
		if i < len(parts2) {
			n2, _ = strconv.Atoi(parts2[i])
		}
		if n1 > n2 {
			return 1
		}
		if n1 < n2 {
			return -1
		}
	}
	return 0
}

// getLatestReleaseInfo 获取完整 release 信息（带缓存）。
//
// force 语义同 getLatestVersion：跳过缓存冷却直接回源，成功后写回并续期。
func getLatestReleaseInfo(force bool) (*githubRelease, error) {
	if !force {
		releaseCacheMutex.RLock()
		if latestReleaseCache != nil && time.Since(latestReleaseCacheTime) < cacheTTL {
			cached := latestReleaseCache
			releaseCacheMutex.RUnlock()
			return cached, nil
		}
		releaseCacheMutex.RUnlock()
	}

	apiURL := "https://api.github.com/repos/shuangji66/fluxor/releases/latest"
	resp, err := ghAPIClient.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	rel.TagName = strings.TrimPrefix(rel.TagName, "v")

	storeReleaseCache(&rel)

	return &rel, nil
}

// storeReleaseCache 把一次成功的 release 查询结果写入缓存。
//
// 同时续期「仅版本号」那份缓存：它与 release 缓存查的是同一个接口，
// 只是 release 信息拿不到时的回退路径。两边一起续期，冷却窗口才是一个整体——
// 否则手动检查刚回源拿到的新版本，一旦 release 接口随后失败，
// 回退路径仍会读到手动检查之前的旧版本号。
func storeReleaseCache(rel *githubRelease) {
	releaseCacheMutex.Lock()
	latestReleaseCache = rel
	latestReleaseCacheTime = time.Now()
	releaseCacheMutex.Unlock()

	cacheMutex.Lock()
	latestVersionCache = rel.TagName
	latestVersionCacheTime = time.Now()
	cacheMutex.Unlock()
}
