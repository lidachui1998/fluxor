package appupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fluxor/internal/buildinfo"
	"fluxor/internal/config"
	"fluxor/internal/httpx"
	"fluxor/internal/logx"
	"fluxor/internal/netinfo"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// selfUpdateRetries 每种下载方式的重试次数：自身代理 2 次、直连 2 次（首次 + 1 次重试）。
	selfUpdateRetries = 2
	// selfUpdateRetryDelay 同一方式重试前的等待时间。
	selfUpdateRetryDelay = time.Second
	// selfUpdateProxyTimeout 经自身代理的单次下载超时上限。
	selfUpdateProxyTimeout = 15 * time.Second
	// selfUpdateDirectTimeout 直连的单次下载超时上限。
	// 直连往往要跨跨境链路，给得比代理宽一些。
	selfUpdateDirectTimeout = 30 * time.Second
	// selfUpdateBinaryName 压缩包内二进制文件名（发布产物固定以此名打包）。
	selfUpdateBinaryName = "fluxor"
	// selfUpdateMaxBinarySize 解压产物上限，避免异常压缩包写满磁盘。
	selfUpdateMaxBinarySize int64 = 256 << 20
	// checksumsAssetName 发布产物放的校验和清单文件名（sha256sum 格式）。
	checksumsAssetName = "checksums.txt"
	// checksumsMaxSize 校验和清单的读取上限（它只有几行，200 字节量级）。
	checksumsMaxSize int64 = 64 << 10
)

// selfUpdateTimeout 返回该下载方式的单次超时上限：经代理 15s，直连 30s。
func selfUpdateTimeout(proxyAddr string) time.Duration {
	if proxyAddr != "" {
		return selfUpdateProxyTimeout
	}
	return selfUpdateDirectTimeout
}

// selfUpdateArchMap 运行架构 → 发布产物架构后缀。
// arm 走 arm64：上游只发布 arm64 产物（沿用旧行为，避免老设备更新链路断裂）。
var selfUpdateArchMap = map[string]string{
	"amd64":  "amd64",
	"x86_64": "amd64",
	"x86":    "amd64",
	"arm64":  "arm64",
	"arm":    "arm64",
}

// releaseAsset 一个可下载的发布产物：tar.gz 压缩包或裸二进制。
type releaseAsset struct {
	name     string
	url      string
	archived bool
}

// assetCandidates 按优先级返回该架构的候选产物：先 tar.gz 压缩包（体积小、不易被中途截断，
// 下载失败风险更低），再回退到裸二进制（兼容尚未打包压缩包的旧 Release）。
// 该架构没有任何候选时返回空切片。
func assetCandidates(rel *githubRelease, goarch string) []releaseAsset {
	suffix := selfUpdateArchMap[goarch]
	if suffix == "" {
		return nil
	}
	base := "fluxor-" + suffix
	wanted := []struct {
		name     string
		archived bool
	}{
		{base + ".tar.gz", true},
		{base + ".tgz", true},
		{base, false},
	}

	byName := make(map[string]string, len(rel.Assets))
	for _, a := range rel.Assets {
		byName[a.Name] = a.BrowserDownloadURL
	}

	var out []releaseAsset
	for _, w := range wanted {
		if u, ok := byName[w.name]; ok && u != "" {
			out = append(out, releaseAsset{name: w.name, url: u, archived: w.archived})
		}
	}
	return out
}

// downloadOnce 执行单次下载：proxyAddr 非空表示经该代理下载（单次 15s），
// 为空则直连（单次 30s）；成功前先清空 dst。
func downloadOnce(dst *os.File, rawURL string, proxyAddr string) error {
	client := &http.Client{
		Timeout: selfUpdateTimeout(proxyAddr),
	}
	if proxyAddr != "" {
		proxyURL, err := url.Parse(proxyAddr)
		if err != nil {
			return err
		}
		client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	}

	resp, err := client.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 重置文件指针并清空内容
	if _, err := dst.Seek(0, 0); err != nil {
		return err
	}
	if err := dst.Truncate(0); err != nil {
		return err
	}

	_, err = io.Copy(dst, resp.Body)
	return err
}

// resetFile 清空文件，供下一次尝试复用同一个文件句柄。
func resetFile(dst *os.File) error {
	if _, err := dst.Seek(0, 0); err != nil {
		return err
	}
	return dst.Truncate(0)
}

// downloadReleaseAsset 下载发布产物：优先经自身代理（本机内核的 mixed/HTTP 端口），
// 失败后回退直连；两种方式各自尝试 selfUpdateRetries 次。
// 单次超时按方式区分：经代理 15s、直连 30s。
// 全部失败时返回带「请检查代理或网络连接」提示的错误。
//
// 更新链路只有「自身代理」与「直连」两条，不再有任何加速源（gh-proxy 等）。
// 单次更新的下载上限：有代理时 2×15s + 2×30s = 90s；内核未运行时（无代理）2×30s = 60s；
// 压缩包与裸二进制两个候选串行，最坏情况再乘 2。
func downloadReleaseAsset(dst *os.File, rawURL string, proxyAddr string) error {
	type method struct {
		label string
		proxy string
	}
	methods := make([]method, 0, 2)
	if proxyAddr != "" {
		methods = append(methods, method{label: "自身代理", proxy: proxyAddr})
	}
	methods = append(methods, method{label: "直连", proxy: ""})

	var failures []string
	for _, m := range methods {
		var lastErr error
		for attempt := 1; attempt <= selfUpdateRetries; attempt++ {
			if attempt > 1 {
				time.Sleep(selfUpdateRetryDelay)
			}
			lastErr = downloadOnce(dst, rawURL, m.proxy)
			if lastErr == nil {
				return nil
			}
			// 失败后重置文件以便下次重试
			if err := resetFile(dst); err != nil {
				return err
			}
		}
		failures = append(failures, fmt.Sprintf("%s %d 次均失败: %v", m.label, selfUpdateRetries, lastErr))
	}
	return fmt.Errorf("请检查代理或网络连接（%s）", strings.Join(failures, "；"))
}

// fetchReleaseAsset 下载候选产物；若为压缩包则解出其中的二进制，
// 返回可执行二进制的落盘路径。
func fetchReleaseAsset(dir string, asset releaseAsset, proxyAddr string, rel *githubRelease) (string, error) {
	downloadPath := filepath.Join(dir, asset.name)
	f, err := os.OpenFile(downloadPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	if err := downloadReleaseAsset(f, asset.url, proxyAddr); err != nil {
		f.Close()
		return "", fmt.Errorf("%s 下载失败，%w", asset.name, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	// 先校验完整性再解压/安装：下载链路会经过「自身代理 → 直连」两条路径，产物是
	// 可执行的完整面板二进制，装错或装坏的代价是面板再也起不来。
	if err := verifyAssetChecksum(rel, asset.name, downloadPath, proxyAddr); err != nil {
		return "", err
	}

	// 两条路径都要过可执行文件校验：裸二进制直接就是安装源，压缩包则先解出二进制。
	binaryPath := downloadPath
	if asset.archived {
		binaryPath = filepath.Join(dir, selfUpdateBinaryName)
		if err := extractBinaryFromArchive(downloadPath, binaryPath); err != nil {
			return "", fmt.Errorf("%s 解压失败: %w", asset.name, err)
		}
	}
	if err := validateExecutableHeader(binaryPath); err != nil {
		return "", fmt.Errorf("%s: %w", asset.name, err)
	}
	return binaryPath, nil
}

// verifyAssetChecksum 用发布产物里的 checksums.txt 校验已下载文件的 sha256。
//
// 返回值语义：
//   - nil：校验通过，或该 Release 没有提供 checksums.txt（历史产物，无从校验）；
//   - error：清单存在但校验不通过、或清单本身取不到——此时必须放弃安装。
//
// 「没有清单」只记警告并继续：把缺清单当成致命错误会让历史版本的更新链路直接断掉，
// 而这道校验的目的是提高门槛，不是自锁。
func verifyAssetChecksum(rel *githubRelease, assetName, path, proxyAddr string) error {
	if rel == nil {
		return nil
	}
	sum, found, err := lookupChecksum(rel, assetName, proxyAddr)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", checksumsAssetName, err)
	}
	if !found {
		logx.Warn(logx.ModuleUpdate,
			"release %s does not provide %s, skipping checksum verification for %s",
			rel.TagName, checksumsAssetName, assetName)
		return nil
	}
	got, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("计算 %s 的校验和失败: %w", assetName, err)
	}
	if !strings.EqualFold(got, sum) {
		return fmt.Errorf("%s 校验和不匹配（期望 %s，实际 %s），已放弃安装", assetName, sum, got)
	}
	logx.Info(logx.ModuleUpdate, "checksum verified: %s sha256=%s", assetName, got)
	return nil
}

// lookupChecksum 下载 checksums.txt 并取出指定文件名的 sha256。
//
// 该文件由发布 CI 生成（sha256sum 的输出格式：<hex>  <文件名>），本函数两种常见写法
// 都兼容（"name" 与 "*name"）。
func lookupChecksum(rel *githubRelease, assetName, proxyAddr string) (string, bool, error) {
	url := ""
	for _, a := range rel.Assets {
		if a.Name == checksumsAssetName {
			url = a.BrowserDownloadURL
			break
		}
	}
	if url == "" {
		return "", false, nil
	}
	content, err := downloadSmallFile(url, proxyAddr, checksumsMaxSize)
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == assetName {
			return fields[0], true, nil
		}
	}
	// 清单在，但没有这一项：同样无法校验，按「没有清单」处理（记警告继续）
	logx.Warn(logx.ModuleUpdate, "%s does not contain an entry for %s", checksumsAssetName, assetName)
	return "", false, nil
}

// downloadSmallFile 下载一个小文件（鉴权清单），沿用主产物的两条链路：自身代理 → 直连。
func downloadSmallFile(rawURL, proxyAddr string, max int64) ([]byte, error) {
	tmp, err := os.CreateTemp("", "fluxor-meta-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		_ = os.Remove(tmpName)
	}()

	proxies := []string{}
	if proxyAddr != "" {
		proxies = append(proxies, proxyAddr)
	}
	proxies = append(proxies, "")

	var lastErr error
	for _, proxy := range proxies {
		if err := downloadOnce(tmp, rawURL, proxy); err != nil {
			lastErr = err
			continue
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return httpx.ReadAllLimited(tmp, max)
	}
	return nil, lastErr
}

// fileSHA256 计算文件的 sha256（十六进制小写）。
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// stageBinary 把下载好的二进制复制到**目标目录下**的临时文件，返回该临时文件路径。
//
// 刻意不用 /tmp：下一步要用 rename 原子就位，而 rename 只有在同一文件系统内才是原子的，
// 很多部署里 /tmp 是独立的 tmpfs。写到目标目录也保证复制期间目标路径上的旧版本始终完好。
//
// 复制完成后 fsync：否则断电可能留下一个「长度正确但内容为零」的文件，而它随后会被
// rename 成正式的可执行文件。
func stageBinary(src, targetPath string) (string, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer srcFile.Close()

	staged := targetPath + ".new"
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return "", err
	}
	cleanup := func(e error) (string, error) {
		_ = out.Close()
		_ = os.Remove(staged)
		return "", e
	}
	if _, err := io.Copy(out, srcFile); err != nil {
		return cleanup(err)
	}
	if err := out.Sync(); err != nil {
		return cleanup(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(staged)
		return "", err
	}
	if err := os.Chmod(staged, 0755); err != nil {
		_ = os.Remove(staged)
		return "", err
	}
	return staged, nil
}

// validateExecutableHeader 校验文件确实是目标平台的可执行文件（ELF 魔数）。
//
// 补的是「解压回退」留下的口子：压缩包里取不到名为 fluxor 的成员时，会退而取
// 「包内第一个非空常规文件」。若上游打包方式改变，第一个成员可能是 README 或 LICENSE，
// 那样会把一个文本文件安装成 fluxor 并 chmod 0755，重启后 exec 失败——面板再也起不来。
//
// 本项目发布产物固定是 linux 二进制（CI 里 GOOS=linux），因此只认 ELF。
func validateExecutableHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return fmt.Errorf("可执行文件头读取失败: %w", err)
	}
	if !bytes.Equal(magic[:], []byte{0x7f, 'E', 'L', 'F'}) {
		return fmt.Errorf("下载到的文件不是可执行文件（ELF 魔数不匹配），已放弃安装")
	}
	return nil
}

// extractBinaryFromArchive 从 tar.gz 压缩包中提取二进制并写入 dstPath。
// 优先取包内名为 fluxor 的常规文件；取不到时回退为包内第一个非空常规文件，
// 以兼容以其它名字打包的历史产物。
func extractBinaryFromArchive(archivePath, dstPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("不是有效的 tar.gz 压缩包: %w", err)
	}
	defer gz.Close()

	for _, exactName := range []bool{true, false} {
		if !exactName {
			// 第二遍：回到流首重新遍历，放宽为「包内第一个非空常规文件」
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			if err := gz.Reset(f); err != nil {
				return fmt.Errorf("不是有效的 tar.gz 压缩包: %w", err)
			}
		}
		found, err := extractFirstRegularFile(gz, dstPath, exactName)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
	}
	return fmt.Errorf("压缩包内未找到可执行文件 %s", selfUpdateBinaryName)
}

// extractFirstRegularFile 顺序扫描 tar 流，把第一个符合条件的常规文件写入 dstPath。
// 目录、符号链接、PAX 头等非普通文件一律跳过，不落盘。
func extractFirstRegularFile(r io.Reader, dstPath string, exactName bool) (bool, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("读取压缩包失败: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
			continue
		}
		if exactName && base != selfUpdateBinaryName {
			continue
		}
		if hdr.Size <= 0 {
			continue
		}
		if hdr.Size > selfUpdateMaxBinarySize {
			return false, fmt.Errorf("压缩包内文件过大（%d 字节）", hdr.Size)
		}

		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
		if err != nil {
			return false, err
		}
		// LimitReader 兜底：hdr.Size 可能被伪造
		n, cErr := io.Copy(out, io.LimitReader(tr, selfUpdateMaxBinarySize+1))
		if err := out.Close(); err != nil && cErr == nil {
			cErr = err
		}
		if cErr != nil {
			return false, fmt.Errorf("解压失败: %w", cErr)
		}
		if n > selfUpdateMaxBinarySize {
			return false, fmt.Errorf("压缩包内文件过大")
		}
		return true, nil
	}
}

// HandleSelfUpdate 更新自身
func HandleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// 用缓存里的 Release 信息即可：能点到「更新版本」说明刚才那次手动检查
	// （?force=1）已经把缓存连同冷却一起续期过，这里没必要再打一次 GitHub。
	rel, err := getLatestReleaseInfo(false)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "获取最新版本信息失败，请检查代理或网络连接: "+err.Error())
		return
	}

	// 当前版本取自编译期注入的 buildinfo.Version（不再由前端经 ?current= 传入）。
	current := stripVersionSuffix(buildinfo.Name())
	if !buildinfo.IsKnown() {
		httpx.WriteJSONError(w, http.StatusBadRequest, "当前版本未知（构建时未注入版本号），无法自更新")
		return
	}
	if compareVersions(rel.TagName, current) <= 0 {
		httpx.WriteJSONError(w, http.StatusBadRequest, "当前已是最新版本，无需更新")
		return
	}

	// 确定目标路径
	targetPath := filepath.Join(config.FluxorBinDir, "fluxor")
	backupDir := filepath.Join(config.FluxorBinDir, "fluxor-backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "创建备份目录失败: "+err.Error())
		return
	}

	// 架构匹配
	suffix, ok := selfUpdateArchMap[runtime.GOARCH]
	if !ok {
		httpx.WriteJSONError(w, http.StatusBadRequest, "不支持的架构: "+runtime.GOARCH)
		return
	}

	// 候选产物：tar.gz 压缩包优先，裸二进制回退
	candidates := assetCandidates(rel, runtime.GOARCH)
	if len(candidates) == 0 {
		httpx.WriteJSONError(w, http.StatusNotFound, "未找到对应架构的发布文件: fluxor-"+suffix)
		return
	}

	// 获取代理端口（内核未运行时取不到，此时只走直连）
	proxyAddr := ""
	if proxyPort := netinfo.GetProxyPortFromConfig(); proxyPort > 0 {
		proxyAddr = fmt.Sprintf("http://127.0.0.1:%d", proxyPort)
	}

	tmpDir, err := os.MkdirTemp("", "fluxor-update-*")
	if err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "创建临时目录失败: "+err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)

	var (
		binaryPath string
		attempted  []string
		lastErr    error
	)
	for _, cand := range candidates {
		attempted = append(attempted, cand.name)
		path, err := fetchReleaseAsset(tmpDir, cand, proxyAddr, rel)
		if err != nil {
			lastErr = err
			continue
		}
		binaryPath = path
		break
	}
	if binaryPath == "" {
		httpx.WriteJSONError(w, http.StatusInternalServerError,
			fmt.Sprintf("下载失败，请检查代理或网络连接。已尝试 %s：%v", strings.Join(attempted, "、"), lastErr))
		return
	}

	// 安装：先把新二进制落到目标目录下的临时文件，再用两次 rename 完成「备份旧版本 +
	// 就位新版本」。全过程目标路径要么指向完整的旧版本、要么指向完整的新版本，不存在
	// 「被截断一半」的中间态——旧实现直接 os.Create(targetPath) 就地覆写，中途失败
	// （磁盘满、进程被杀）会留下一个坏掉的 fluxor，面板重启即失败。
	staged, err := stageBinary(binaryPath, targetPath)
	if err != nil {
		logx.Error(logx.ModuleUpdate, "failed to stage new binary: %v", err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "准备新版本文件失败: "+err.Error())
		return
	}

	backupName := filepath.Join(backupDir, "fluxor")
	hadPrevious := false
	if _, err := os.Stat(targetPath); err == nil {
		if err := os.Rename(targetPath, backupName); err != nil {
			_ = os.Remove(staged)
			httpx.WriteJSONError(w, http.StatusInternalServerError, "备份旧文件失败: "+err.Error())
			return
		}
		hadPrevious = true
	}

	if err := os.Rename(staged, targetPath); err != nil {
		_ = os.Remove(staged)
		// 就位失败就把备份放回去：宁可停在旧版本，也不能让目标路径空着
		if hadPrevious {
			if rbErr := os.Rename(backupName, targetPath); rbErr != nil {
				logx.Error(logx.ModuleUpdate,
					"failed to restore the previous binary after install failure; %s must be restored manually from %s: %v",
					targetPath, backupName, rbErr)
			}
		}
		httpx.WriteJSONError(w, http.StatusInternalServerError, "安装新版本失败: "+err.Error())
		return
	}

	if err := os.Chmod(targetPath, 0755); err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "设置目标文件权限失败: "+err.Error())
		return
	}

	// 响应成功
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "更新成功，即将重启"})

	// 启动新进程并退出
	go func() {
		time.Sleep(200 * time.Millisecond)
		cmd := exec.Command(targetPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		if err := cmd.Start(); err != nil {
			logx.Error(logx.ModuleUpdate, "failed to restart after self-update: %v", err)
			return
		}
		os.Exit(0)
	}()
}
