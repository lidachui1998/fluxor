package logx

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// recordPattern 锁定记录格式：本地时间戳（毫秒）+ 等级 + 方括号模块 + 正文。
var recordPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} (DEBUG|INFO |WARN |ERROR) \[[A-Z]+\] .+$`)

// readLog 读取日志文件内容（Setup 之后由 logx 独占写入）。
func readLog(t *testing.T, path string) string {
	t.Helper()
	Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	return string(data)
}

func lines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// TestRecordFormat 校验记录格式、单行输出与可选参数缺失时不做格式化。
func TestRecordFormat(t *testing.T) {
	defer resetState()

	path := filepath.Join(t.TempDir(), "fluxor.log")
	if err := Setup(path); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	SetLevel(LevelDebug)

	Debug(ModuleMain, "plain debug line")
	Info(ModuleCore, "mihomo started: pid=%d bin=%s", 1234, "/usr/bin/mihomo")
	Warn(ModuleSub, "subscription skipped: name=%q", "demo")
	Error(ModuleTproxy, "failed to apply rules: %v", "no such table")
	// 正文里的百分号必须经参数传入：无参数时 logx 不做格式化，裸 % 会被 vet 判为缺动词
	Info(ModuleNet, "progress %s", "100%")
	Info(ModuleWeb, "trailing newline stripped\n")

	out := readLog(t, path)
	got := lines(out)
	if len(got) != 6 {
		t.Fatalf("want 6 records, got %d:\n%s", len(got), out)
	}
	for _, line := range got {
		if !recordPattern.MatchString(line) {
			t.Errorf("record does not match format: %q", line)
		}
	}
	if !strings.Contains(got[2], "[SUB] subscription skipped") {
		t.Errorf("module/message mismatch: %q", got[2])
	}
	if !strings.Contains(got[4], "progress 100%") {
		t.Errorf("percent sign in argument was mangled: %q", got[4])
	}
	if strings.Contains(got[5], "\n") {
		t.Errorf("trailing newline was not stripped: %q", got[5])
	}
}

// TestLevelFilter 校验等级过滤：低于当前等级的记录整体丢弃。
func TestLevelFilter(t *testing.T) {
	defer resetState()

	path := filepath.Join(t.TempDir(), "fluxor.log")
	if err := Setup(path); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	SetLevel(LevelWarn)

	Debug(ModuleMain, "dropped debug")
	Info(ModuleMain, "dropped info")
	Warn(ModuleMain, "kept warn")
	Error(ModuleMain, "kept error")

	got := lines(readLog(t, path))
	if len(got) != 2 {
		t.Fatalf("want 2 records, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "WARN ") || !strings.Contains(got[1], "ERROR") {
		t.Errorf("unexpected records kept: %v", got)
	}
}

// TestParseLevel 校验环境变量取值解析（大小写不敏感、脏值回落 INFO）。
func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug": LevelDebug, "DEBUG": LevelDebug, " Debug ": LevelDebug,
		"info": LevelInfo, "": LevelInfo, "bogus": LevelInfo,
		"warn": LevelWarn, "warning": LevelWarn, "WARN": LevelWarn,
		"error": LevelError, "Error": LevelError,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, LevelName(got), LevelName(want))
		}
	}
}

// TestSetupFailureKeepsStderr 校验日志目录不可写时不 panic、且未启用文件双写。
func TestSetupFailureKeepsStderr(t *testing.T) {
	defer resetState()

	// 用一个「以普通文件充当目录」的路径，必然创建目录失败
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("prepare blocker: %v", err)
	}
	if err := Setup(filepath.Join(blocker, "fluxor.log")); err == nil {
		t.Fatal("want error for uncreatable log directory, got nil")
	}
	if Enabled() {
		t.Error("file sink must stay disabled after Setup failure")
	}
	Info(ModuleMain, "still loggable to stderr only")
}

// resetState 恢复包级状态，避免用例之间互相影响。
func resetState() {
	Close()
	SetLevel(LevelInfo)
}
