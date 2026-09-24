package appupdate

import (
	"encoding/json"
	"fmt"
	"math"
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

// compareVersions 比较两个版本号：前者大于后者返回 1，小于返回 -1，相等返回 0。
//
// 按语义化版本的规则处理，而不是「按 . 切开逐个 Atoi」：
//   - 去掉可能存在的 v/V 前缀（GitHub tag 与 make V=... 的写法未必一致）；
//   - 段数不等时缺失段按 0 补（1.2 == 1.2.0）；
//   - 忽略构建元数据（+ 之后）；
//   - **预发布版本小于同版本的正式版**（1.1.0-rc1 < 1.1.0），预发布之间按 semver
//     的标识符规则比较（纯数字段按数值比，数字段小于非数字段）。
//
// 旧实现用 `strconv.Atoi` 且忽略错误，于是 "0-rc1" 这类段被静默当成 0：
// compareVersions("1.1.0", "1.1.0-rc1") == 0，即**跑预发布版的用户永远收不到正式版
// 更新提示**（反向也会把 rc 当成正式版）。这类错误不会报错、只会静默漏报更新。
func compareVersions(v1, v2 string) int {
	core1, pre1 := splitVersion(v1)
	core2, pre2 := splitVersion(v2)

	for i := 0; i < max(len(core1), len(core2)); i++ {
		var n1, n2 int
		if i < len(core1) {
			n1 = core1[i]
		}
		if i < len(core2) {
			n2 = core2[i]
		}
		if n1 != n2 {
			if n1 > n2 {
				return 1
			}
			return -1
		}
	}

	// 核心段相同：有预发布标识的更小
	if len(pre1) == 0 && len(pre2) == 0 {
		return 0
	}
	if len(pre1) == 0 {
		return 1
	}
	if len(pre2) == 0 {
		return -1
	}
	return comparePrerelease(pre1, pre2)
}

// splitVersion 把版本串拆成「数字核心段」与「预发布标识」。
//
// 容忍脏输入：核心段里出现非数字时按其在数值中的前缀解析（"1a" → 1），整段无数字时按 0，
// 这样任何输入都能得到一个确定的可比较结果，不会因为一处格式异常就静默判成相等。
func splitVersion(v string) (core []int, pre string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	// 构建元数据不参与比较（semver：+ 之后的内容可忽略）
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	for _, part := range strings.Split(v, ".") {
		core = append(core, leadingNumber(part))
	}
	return core, pre
}

// leadingNumber 取字符串开头的连续数字（"12b" → 12，"" / "b" → 0）。
func leadingNumber(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		// 超出 int 范围：按最大可比较值处理，避免回落到 0 而把「更大的版本」判小
		return math.MaxInt
	}
	return n
}

// comparePrerelease 按 semver 规则比较两个预发布标识串（形如 "rc.1"、"beta.2"、"alpha"）。
func comparePrerelease(p1, p2 string) int {
	ids1 := strings.Split(p1, ".")
	ids2 := strings.Split(p2, ".")
	for i := 0; i < max(len(ids1), len(ids2)); i++ {
		// 标识符少的一方更小（1.1.0-alpha < 1.1.0-alpha.1）
		if i >= len(ids1) {
			return -1
		}
		if i >= len(ids2) {
			return 1
		}
		if c := comparePrereleaseID(ids1[i], ids2[i]); c != 0 {
			return c
		}
	}
	return 0
}

// comparePrereleaseID 比较单个预发布标识符：纯数字按数值比，且数字标识符小于非数字标识符。
func comparePrereleaseID(a, b string) int {
	aNum, aIsNum := atoiIfNumeric(a)
	bNum, bIsNum := atoiIfNumeric(b)
	switch {
	case aIsNum && bIsNum:
		if aNum != bNum {
			if aNum > bNum {
				return 1
			}
			return -1
		}
		return 0
	case aIsNum:
		return -1 // 数字 < 非数字
	case bIsNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// atoiIfNumeric 判定并解析「纯数字标识符」。
func atoiIfNumeric(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return math.MaxInt, true
	}
	return n, true
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
