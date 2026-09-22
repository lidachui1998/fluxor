package appupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tarEntry 描述一个待写入测试压缩包的条目。
type tarEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
	size     int64 // 仅伪造大小时使用；为 0 时取 len(body)
}

// buildTarGz 生成一个测试用 tar.gz。allowTruncated 为 true 时不写条目内容，
// 用于伪造一个「声明体积超大」的头部。
func buildTarGz(t *testing.T, entries []tarEntry, allowTruncated bool) string {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		size := e.size
		if size == 0 && typeflag == tar.TypeReg {
			size = int64(len(e.body))
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0755,
			Size:     size,
			Typeflag: typeflag,
			Linkname: e.linkname,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("写 tar 头失败: %v", err)
		}
		if typeflag != tar.TypeReg || allowTruncated {
			continue
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("写 tar 内容失败: %v", err)
		}
	}
	// 伪造大小时故意不补齐内容，Close 必然报错，此处忽略
	_ = tw.Close()
	if err := gz.Close(); err != nil {
		t.Fatalf("关闭 gzip 失败: %v", err)
	}

	path := filepath.Join(t.TempDir(), "asset.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("写压缩包失败: %v", err)
	}
	return path
}

// testRelease 用 GitHub API 的真实 JSON 形状构造 release，
// 顺带校验 `assets[].browser_download_url` 等标签未写错。
func testRelease(t *testing.T, assetNames ...string) *githubRelease {
	t.Helper()

	assets := make([]string, 0, len(assetNames))
	for _, n := range assetNames {
		assets = append(assets, fmt.Sprintf(`{"name":%q,"browser_download_url":"https://example.com/%s"}`, n, n))
	}
	body := fmt.Sprintf(`{"tag_name":"v1.0.0","body":"notes","assets":[%s]}`, strings.Join(assets, ","))

	var rel githubRelease
	if err := json.Unmarshal([]byte(body), &rel); err != nil {
		t.Fatalf("解析测试 release 失败: %v", err)
	}
	return &rel
}

func TestAssetCandidatesPrefersArchive(t *testing.T) {
	rel := testRelease(t,
		"fluxor-amd64",
		"fluxor-arm64",
		"fluxor-amd64.tar.gz",
		"fluxor-arm64.tar.gz",
	)

	got := assetCandidates(rel, "amd64")
	if len(got) != 2 {
		t.Fatalf("amd64 候选数 = %d, 期望 2: %+v", len(got), got)
	}
	if got[0].name != "fluxor-amd64.tar.gz" || !got[0].archived {
		t.Errorf("首选应为 tar.gz 压缩包，实际 %+v", got[0])
	}
	if got[1].name != "fluxor-amd64" || got[1].archived {
		t.Errorf("回退应为裸二进制，实际 %+v", got[1])
	}

	// 别名架构映射
	if got := assetCandidates(rel, "x86_64"); len(got) != 2 || got[0].name != "fluxor-amd64.tar.gz" {
		t.Errorf("x86_64 应映射到 amd64 候选，实际 %+v", got)
	}
	if got := assetCandidates(rel, "arm"); len(got) != 2 || got[0].name != "fluxor-arm64.tar.gz" {
		t.Errorf("arm 应映射到 arm64 候选，实际 %+v", got)
	}
	if got := assetCandidates(rel, "riscv64"); got != nil {
		t.Errorf("未知架构应无候选，实际 %+v", got)
	}
}

// 兼容老版本更新机制：Release 只有裸二进制时，仍能取到候选。
func TestAssetCandidatesBinaryOnlyRelease(t *testing.T) {
	rel := testRelease(t, "fluxor-arm64")

	got := assetCandidates(rel, "arm64")
	if len(got) != 1 || got[0].name != "fluxor-arm64" || got[0].archived {
		t.Fatalf("裸二进制回退候选不正确: %+v", got)
	}
}

func TestExtractBinaryFromArchivePrefersNamedBinary(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "fluxor-amd64", body: "WRONG"},
		{name: "LICENSE", body: "license"},
		{name: "fluxor", body: "RIGHT"},
	}, false)

	dst := filepath.Join(t.TempDir(), "fluxor")
	if err := extractBinaryFromArchive(archive, dst); err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("读取解压产物失败: %v", err)
	}
	if string(got) != "RIGHT" {
		t.Errorf("应优先提取包内名为 fluxor 的文件，实际 %q", got)
	}
	if fi, err := os.Stat(dst); err != nil {
		t.Fatalf("stat 失败: %v", err)
	} else if fi.Mode().Perm()&0100 == 0 {
		t.Errorf("解压产物应带可执行权限，实际 %v", fi.Mode())
	}
}

// 包内没有名为 fluxor 的文件时，回退为第一个非空常规文件。
func TestExtractBinaryFromArchiveFallsBackToFirstRegularFile(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "notes.txt", body: ""},
		{name: "fluxor-arm64", body: "BIN"},
	}, false)

	dst := filepath.Join(t.TempDir(), "fluxor")
	if err := extractBinaryFromArchive(archive, dst); err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "BIN" {
		t.Errorf("应回退提取第一个非空常规文件，实际 %q", got)
	}
}

// 符号链接不得被跟随：包内伪造指向 /etc/passwd 的链接必须被跳过。
func TestExtractBinaryFromArchiveSkipsSymlink(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "fluxor", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
		{name: "fluxor-amd64", body: "REAL-BIN"},
	}, false)

	dst := filepath.Join(t.TempDir(), "fluxor")
	if err := extractBinaryFromArchive(archive, dst); err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "REAL-BIN" {
		t.Errorf("符号链接必须跳过，实际提取到 %q", got)
	}
}

func TestExtractBinaryFromArchiveRejectsInvalidArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fluxor-amd64.tar.gz")
	if err := os.WriteFile(path, []byte("this is a raw binary, not gzip"), 0644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	if err := extractBinaryFromArchive(path, filepath.Join(t.TempDir(), "fluxor")); err == nil {
		t.Fatal("非 gzip 内容应报错")
	}
}

// 声明体积超过上限的条目必须被拒绝，避免解压耗尽磁盘。
func TestExtractBinaryFromArchiveRejectsOversizedEntry(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "fluxor", size: selfUpdateMaxBinarySize + 1},
	}, true)

	if err := extractBinaryFromArchive(archive, filepath.Join(t.TempDir(), "fluxor")); err == nil {
		t.Fatal("超大条目应报错")
	}
}

// fetchReleaseAsset 的端到端覆盖：压缩包走「下载 + 解压」，裸二进制走「下载即用」。
func TestFetchReleaseAssetArchiveAndBinary(t *testing.T) {
	archivePath := buildTarGz(t, []tarEntry{{name: "fluxor", body: "ARCHIVED-BIN"}}, false)
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("读取测试压缩包失败: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fluxor-amd64.tar.gz":
			w.Write(archiveBytes)
		case "/fluxor-amd64":
			fmt.Fprint(w, "RAW-BIN")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	got, err := fetchReleaseAsset(dir, releaseAsset{
		name:     "fluxor-amd64.tar.gz",
		url:      srv.URL + "/fluxor-amd64.tar.gz",
		archived: true,
	}, "")
	if err != nil {
		t.Fatalf("压缩包路径应成功: %v", err)
	}
	if body, _ := os.ReadFile(got); string(body) != "ARCHIVED-BIN" {
		t.Errorf("压缩包解出内容 = %q, 期望 ARCHIVED-BIN", body)
	}

	got, err = fetchReleaseAsset(dir, releaseAsset{
		name:     "fluxor-amd64",
		url:      srv.URL + "/fluxor-amd64",
		archived: false,
	}, "")
	if err != nil {
		t.Fatalf("裸二进制路径应成功: %v", err)
	}
	if body, _ := os.ReadFile(got); string(body) != "RAW-BIN" {
		t.Errorf("裸二进制内容 = %q, 期望 RAW-BIN", body)
	}
}

// 单次超时按下载方式区分：经自身代理 15s，直连 30s。
func TestSelfUpdateTimeout(t *testing.T) {
	if got := selfUpdateTimeout("http://127.0.0.1:7890"); got != 15*time.Second {
		t.Errorf("经代理单次超时 = %v, 期望 15s", got)
	}
	if got := selfUpdateTimeout(""); got != 30*time.Second {
		t.Errorf("直连单次超时 = %v, 期望 30s", got)
	}
}

func TestDownloadReleaseAssetRetriesThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, "PAYLOAD")
	}))
	defer srv.Close()

	f, err := os.CreateTemp(t.TempDir(), "dl-*")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	defer f.Close()

	if err := downloadReleaseAsset(f, srv.URL+"/asset", ""); err != nil {
		t.Fatalf("第二次尝试应成功: %v", err)
	}
	if calls != 2 {
		t.Errorf("请求次数 = %d, 期望 2", calls)
	}
	got, _ := os.ReadFile(f.Name())
	if string(got) != "PAYLOAD" {
		t.Errorf("下载内容 = %q", got)
	}
}

// 直连 2 次均失败后必须给出「请检查代理或网络连接」的提示。
func TestDownloadReleaseAssetFailureSuggestsProxyOrNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f, err := os.CreateTemp(t.TempDir(), "dl-*")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	defer f.Close()

	err = downloadReleaseAsset(f, srv.URL+"/asset", "")
	if err == nil {
		t.Fatal("全部失败时应返回错误")
	}
	if calls != selfUpdateRetries {
		t.Errorf("直连请求次数 = %d, 期望 %d", calls, selfUpdateRetries)
	}
	msg := err.Error()
	if !strings.Contains(msg, "请检查代理或网络连接") {
		t.Errorf("错误信息缺少代理/网络提示: %s", msg)
	}
	if strings.Contains(msg, "自身代理") {
		t.Errorf("未提供代理时不应声称尝试过自身代理: %s", msg)
	}
	if !strings.Contains(msg, fmt.Sprintf("直连 %d 次均失败", selfUpdateRetries)) {
		t.Errorf("错误信息应说明直连重试次数: %s", msg)
	}
}

// 提供代理时先走代理，代理可用则不再直连。
func TestDownloadReleaseAssetPrefersProxy(t *testing.T) {
	var directCalls int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "VIA-PROXY")
	}))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directCalls++
		fmt.Fprint(w, "DIRECT")
	}))
	defer target.Close()

	f, err := os.CreateTemp(t.TempDir(), "dl-*")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	defer f.Close()

	if err := downloadReleaseAsset(f, target.URL+"/asset", proxy.URL); err != nil {
		t.Fatalf("经代理下载应成功: %v", err)
	}
	got, _ := os.ReadFile(f.Name())
	if string(got) != "VIA-PROXY" {
		t.Errorf("应经代理下载，实际 %q", got)
	}
	if directCalls != 0 {
		t.Errorf("代理成功后不应再直连，直连次数 = %d", directCalls)
	}
}

// 代理失败后必须回退直连。
func TestDownloadReleaseAssetFallsBackToDirect(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "DIRECT-OK")
	}))
	defer target.Close()

	f, err := os.CreateTemp(t.TempDir(), "dl-*")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	defer f.Close()

	if err := downloadReleaseAsset(f, target.URL+"/asset", proxy.URL); err != nil {
		t.Fatalf("代理失败后应回退直连成功: %v", err)
	}
	got, _ := os.ReadFile(f.Name())
	if string(got) != "DIRECT-OK" {
		t.Errorf("回退直连内容 = %q", got)
	}
}
