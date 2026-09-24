package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 迁移的「要么全成功、要么续做」是 P2 修复的核心：
//
// 原实现用「settings.json 是否存在」作为幂等判据，而写入顺序恰好把 settings.json
// 排在第一个。于是从第 2 个文件起失败时，下次启动会整段跳过迁移——未迁移的类别
// 既不落新文件、也不会被读取（旧文件只是被记一条 WARN 忽略），用户看到的就是
// 「规则/隧道/元数据凭空消失」，与注释里「下次启动重试」的承诺相反。
//
// 现在用一个显式的 `.migrating` 标记区分两种情形：标记存在 = 上次写到一半，只补缺失的；
// 标记不存在且 settings.json 在 = 新布局确实在用，旧文件只是残留，保持忽略。

const legacyFixture = `{
  "proxy_port": 7890,
  "panel_port": 9090,
  "tproxy_port": 7898,
  "mode": "switch",
  "rule_group": "base",
  "ui_panel": "metacubexd",
  "active_subscription": "机场A",
  "subscriptions": [
    {
      "name": "机场A",
      "url": "https://example.invalid/a",
      "update_interval": 3600,
      "health_interval": 300,
      "prefix": "",
      "custom_rules": [{"id":"r1","type":"DOMAIN-SUFFIX","payload":"legacy.example","target":"DIRECT","position":"before"}]
    }
  ]
}`

func legacyPath() string { return FluxorConfigFile }
func markerPath() string { return FluxorConfigFile + ".migrating" }

// TestMigrationResumesWhenMarkerPresent 中断过的迁移必须续做：补缺失的几份，
// 且**不覆盖**已经写成的 settings.json。
func TestMigrationResumesWhenMarkerPresent(t *testing.T) {
	useTempDataDir(t)

	writeFileRaw(t, legacyPath(), legacyFixture)
	// 模拟「上次迁移写到 settings.json 之后失败」：settings 已存在、标记仍在、
	// 其余四份还没写。settings 内容刻意与旧文件不同，用来验证续做不会覆盖它。
	const existingSettings = `{"mode":"merge","proxy_port":7891,"panel_port":9091,"tproxy_port":7899,"rule_group":"full","ui_panel":"zashboard","subscriptions":[{"name":"机场B","url":"https://example.invalid/b"}]}`
	writeFileRaw(t, FluxorSettingsFile, existingSettings)
	writeFileRaw(t, markerPath(), "in-progress\n")

	MigrateLegacyFiles()

	// settings.json 必须原样保留
	if got := readFileRaw(t, FluxorSettingsFile); got != existingSettings {
		t.Fatalf("续做迁移不得覆盖已写成的 settings.json\n got=%s\nwant=%s", got, existingSettings)
	}
	// 其余四份必须补齐
	for _, p := range []string{FluxorRulesFile, FluxorTunnelsFile, FluxorMetaFile, FluxorTproxyFile} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("续做迁移应补齐 %s: %v", p, err)
		}
	}
	// 旧文件应被归档，标记应被清除
	if _, err := os.Stat(legacyPath()); !os.IsNotExist(err) {
		t.Fatalf("迁移完成后旧文件应被归档改名: %v", err)
	}
	if _, err := os.Stat(markerPath()); !os.IsNotExist(err) {
		t.Fatalf("迁移完成后标记应被删除: %v", err)
	}
	if !strings.Contains(readFileRaw(t, FluxorRulesFile), "legacy.example") {
		t.Fatal("续做迁移应把旧文件里的自定义规则搬进 rules.json")
	}
}

// TestMigrationSkippedWithoutMarkerWhenNewLayoutInUse 没有标记且 settings.json 已在用时，
// 不得因为旧文件残留而把数据「补」回来（那会复活用户已删除的内容）。
func TestMigrationSkippedWithoutMarkerWhenNewLayoutInUse(t *testing.T) {
	useTempDataDir(t)

	writeFileRaw(t, legacyPath(), legacyFixture)
	const existingSettings = `{"mode":"merge","proxy_port":7890,"panel_port":9090,"tproxy_port":7898,"rule_group":"base","ui_panel":"metacubexd","subscriptions":[]}`
	writeFileRaw(t, FluxorSettingsFile, existingSettings)

	MigrateLegacyFiles()

	if got := readFileRaw(t, FluxorSettingsFile); got != existingSettings {
		t.Fatalf("新布局已在使用时不得改写 settings.json")
	}
	if _, err := os.Stat(FluxorRulesFile); !os.IsNotExist(err) {
		t.Fatal("新布局已在使用时不应从残留的旧文件生成 rules.json")
	}
	if _, err := os.Stat(legacyPath()); err != nil {
		t.Fatalf("残留的旧文件应原样保留（只记日志忽略）: %v", err)
	}
}

// TestMigrationFromScratch 全新迁移：五份文件齐全、旧文件归档、标记清除。
func TestMigrationFromScratch(t *testing.T) {
	useTempDataDir(t)

	writeFileRaw(t, legacyPath(), legacyFixture)

	MigrateLegacyFiles()

	for _, p := range []string{FluxorSettingsFile, FluxorRulesFile, FluxorTunnelsFile, FluxorMetaFile, FluxorTproxyFile} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("首次迁移应生成 %s: %v", p, err)
		}
	}
	if _, err := os.Stat(markerPath()); !os.IsNotExist(err) {
		t.Fatalf("迁移完成后标记应被删除: %v", err)
	}
	matches, _ := filepath.Glob(legacyPath() + ".migrated-*")
	if len(matches) == 0 {
		t.Fatal("旧文件应被归档为 fluxor.json.migrated-<时间戳>")
	}
	if !strings.Contains(readFileRaw(t, FluxorRulesFile), "legacy.example") {
		t.Fatal("自定义规则应被搬进 rules.json")
	}
	if !strings.Contains(readFileRaw(t, FluxorSettingsFile), "机场A") {
		t.Fatal("订阅注册表应被搬进 settings.json")
	}
}

// TestMigrationCorruptLegacyDoesNotBlockStartup 旧文件损坏时按全新安装处理，
// 且不得留下永久阻塞启动的标记。
func TestMigrationCorruptLegacyDoesNotBlockStartup(t *testing.T) {
	useTempDataDir(t)

	writeFileRaw(t, legacyPath(), `{"mode":"switch",`)

	MigrateLegacyFiles()

	if _, err := os.Stat(markerPath()); !os.IsNotExist(err) {
		t.Fatal("解析失败的迁移不应留下标记（否则会一直重试）")
	}
	matches, _ := filepath.Glob(legacyPath() + ".corrupt-*")
	if len(matches) == 0 {
		t.Fatal("损坏的旧文件应留下 .corrupt-* 备份")
	}
}
