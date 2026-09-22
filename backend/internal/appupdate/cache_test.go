package appupdate

import (
	"encoding/json"
	"fluxor/internal/buildinfo"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubGitHub 假 GitHub 接口：统计真实回源次数，并允许测试中途改「远端最新版」。
type stubGitHub struct {
	mu     sync.Mutex
	hits   int
	tag    string
	status int
}

func (s *stubGitHub) setTag(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tag = tag
}

func (s *stubGitHub) setStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// bump 记一次回源并返回本次响应内容。
func (s *stubGitHub) bump() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits++
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	return status, s.tag
}

func (s *stubGitHub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

// stubGitHubTransport 把全部 GitHub 请求就地应答，不出网。
type stubGitHubTransport struct{ stub *stubGitHub }

func (t *stubGitHubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	status, tag := t.stub.bump()
	body := "{}"
	if status == http.StatusOK {
		body = fmt.Sprintf(`{"tag_name":%q,"body":"notes","assets":[]}`, tag)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// withStubGitHub 用假客户端替换包级 ghAPIClient，并清空 Fluxor 版本缓存
// （都是包级状态，测试之间必须复位；本包测试不并发执行）。
func withStubGitHub(t *testing.T, tag string) *stubGitHub {
	t.Helper()

	stub := &stubGitHub{tag: tag}
	oldClient := ghAPIClient
	ghAPIClient = &http.Client{Transport: &stubGitHubTransport{stub: stub}}
	resetFluxorVersionCaches()

	t.Cleanup(func() {
		ghAPIClient = oldClient
		resetFluxorVersionCaches()
	})
	return stub
}

func resetFluxorVersionCaches() {
	releaseCacheMutex.Lock()
	latestReleaseCache = nil
	latestReleaseCacheTime = time.Time{}
	releaseCacheMutex.Unlock()

	cacheMutex.Lock()
	latestVersionCache = ""
	latestVersionCacheTime = time.Time{}
	cacheMutex.Unlock()
}

// TestReleaseInfoCacheHonoursForce 手动检查（force）无视冷却回源，成功后冷却重新起算。
func TestReleaseInfoCacheHonoursForce(t *testing.T) {
	stub := withStubGitHub(t, "1.2.3")

	rel, err := getLatestReleaseInfo(false)
	if err != nil || rel.TagName != "1.2.3" {
		t.Fatalf("首次查询应回源拿到 1.2.3，得到 %v / err=%v", rel, err)
	}
	if _, err := getLatestReleaseInfo(false); err != nil {
		t.Fatalf("第二次查询出错: %v", err)
	}
	if got := stub.count(); got != 1 {
		t.Fatalf("冷却期内不应重复回源，实际回源 %d 次", got)
	}

	// 远端发布新版本：不带 force 仍读缓存（这正是被冷却挡住的表现）
	stub.setTag("1.3.0")
	rel, err = getLatestReleaseInfo(false)
	if err != nil || rel.TagName != "1.2.3" || stub.count() != 1 {
		t.Fatalf("冷却期内应返回缓存 1.2.3，得到 %v / err=%v / 回源 %d 次", rel, err, stub.count())
	}

	// 手动检查：无视冷却直接回源
	rel, err = getLatestReleaseInfo(true)
	if err != nil || rel.TagName != "1.3.0" {
		t.Fatalf("force 查询应回源拿到 1.3.0，得到 %v / err=%v", rel, err)
	}
	if got := stub.count(); got != 2 {
		t.Fatalf("force 查询应回源一次，实际回源 %d 次", got)
	}

	// 冷却已续期：随后的自动检查直接用刚查询到的结果，不再回源
	rel, err = getLatestReleaseInfo(false)
	if err != nil || rel.TagName != "1.3.0" || stub.count() != 2 {
		t.Fatalf("force 后自动检查应命中续期缓存 1.3.0，得到 %v / err=%v / 回源 %d 次", rel, err, stub.count())
	}

	// 「仅版本号」缓存一并续期：release 信息拿不到时的回退路径不会读到旧版本号
	version, err := getLatestVersion(false)
	if err != nil || version != "1.3.0" || stub.count() != 2 {
		t.Fatalf("版本号缓存应随手动检查续期为 1.3.0，得到 %q / err=%v / 回源 %d 次", version, err, stub.count())
	}
}

// TestVersionCacheHonoursForce 回退路径（仅版本号）的 force 语义与 release 信息一致。
func TestVersionCacheHonoursForce(t *testing.T) {
	stub := withStubGitHub(t, "2.0.0")

	if version, err := getLatestVersion(false); err != nil || version != "2.0.0" {
		t.Fatalf("首次查询应回源拿到 2.0.0，得到 %q / err=%v", version, err)
	}

	stub.setTag("2.1.0")
	if version, err := getLatestVersion(false); err != nil || version != "2.0.0" || stub.count() != 1 {
		t.Fatalf("冷却期内应返回缓存 2.0.0，得到 %q / err=%v / 回源 %d 次", version, err, stub.count())
	}

	if version, err := getLatestVersion(true); err != nil || version != "2.1.0" {
		t.Fatalf("force 查询应回源拿到 2.1.0，得到 %q / err=%v", version, err)
	}

	if version, err := getLatestVersion(false); err != nil || version != "2.1.0" || stub.count() != 2 {
		t.Fatalf("force 后自动检查应命中续期缓存 2.1.0，得到 %q / err=%v / 回源 %d 次", version, err, stub.count())
	}
}

// TestForceCheckFailureKeepsOldCache 手动检查失败时不伪装成功、也不冲掉已有缓存。
func TestForceCheckFailureKeepsOldCache(t *testing.T) {
	stub := withStubGitHub(t, "1.0.0")

	if _, err := getLatestReleaseInfo(false); err != nil {
		t.Fatalf("预热缓存失败: %v", err)
	}

	// GitHub 限流：手动检查必须如实回报错误
	stub.setStatus(http.StatusForbidden)
	if _, err := getLatestReleaseInfo(true); err == nil {
		t.Fatal("限流时应回报错误，而不是返回空结果")
	}
	if _, err := getLatestVersion(true); err == nil {
		t.Fatal("限流时回退路径也应回报错误")
	}

	// 旧缓存仍在（未被空结果覆盖），自动检查继续可用
	rel, err := getLatestReleaseInfo(false)
	if err != nil || rel.TagName != "1.0.0" {
		t.Fatalf("失败的手动检查不应破坏已有缓存，得到 %v / err=%v", rel, err)
	}
}

// TestHandleCheckUpdateForceBypassesCooldown 端到端：弹窗按钮带 ?force=1 能立刻看到新版本，
// 随后不带 force 的自动检查沿用这次结果且不再回源。
func TestHandleCheckUpdateForceBypassesCooldown(t *testing.T) {
	stub := withStubGitHub(t, "1.0.0")

	oldVersion := buildinfo.Version
	buildinfo.Version = "1.0.0"
	t.Cleanup(func() { buildinfo.Version = oldVersion })

	// 1. 自动检查（无 force）：回源并落缓存
	if got := doCheckUpdate(t, ""); got["hasUpdate"] != false {
		t.Fatalf("尚无新版本时 hasUpdate 应为 false，得到 %v", got)
	}
	if got := stub.count(); got != 1 {
		t.Fatalf("自动检查应回源一次，实际 %d 次", got)
	}

	// 2. 远端发布 1.1.0：自动检查被冷却挡住，仍报「已是最新」
	stub.setTag("1.1.0")
	if got := doCheckUpdate(t, ""); got["hasUpdate"] != false || stub.count() != 1 {
		t.Fatalf("冷却期内自动检查应沿用缓存，得到 %v / 回源 %d 次", got, stub.count())
	}

	// 3. 手动检查：无视冷却，立刻看到 1.1.0 与更新日志
	got := doCheckUpdate(t, "?force=1")
	if got["hasUpdate"] != true || got["latest"] != "1.1.0" {
		t.Fatalf("force 检查应发现 1.1.0，得到 %v", got)
	}
	if got["releaseNotes"] != "notes" {
		t.Fatalf("force 检查应带回更新日志，得到 %v", got["releaseNotes"])
	}
	if got := stub.count(); got != 2 {
		t.Fatalf("force 检查应回源一次，实际共 %d 次", got)
	}

	// 4. 冷却已续期：自动检查不再回源，且结果与手动检查一致
	if got := doCheckUpdate(t, ""); got["hasUpdate"] != true || got["latest"] != "1.1.0" || stub.count() != 2 {
		t.Fatalf("续期后的自动检查应沿用 1.1.0，得到 %v / 回源 %d 次", got, stub.count())
	}
}

// doCheckUpdate 直接调用 /check-update 的 handler，query 形如 "?force=1"。
func doCheckUpdate(t *testing.T, query string) map[string]interface{} {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/check-update"+query, nil)
	rec := httptest.NewRecorder()
	HandleCheckUpdate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("检查更新返回 %d: %s", rec.Code, rec.Body.String())
	}

	var got map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return got
}
