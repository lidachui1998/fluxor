package config

import (
	"path/filepath"
	"testing"
)

// TestSetDataDirDerivesRuntimeFiles 锁定「统一运行数据目录」不变量：
// fluxor.json 与 fluxor.log 必须落在该目录下，PID 文件默认与之同址
// （未按模式固定时），且不再有单项覆盖的入口。
func TestSetDataDirDerivesRuntimeFiles(t *testing.T) {
	defer capturePaths()()

	pidDirPinned = ""
	dir := t.TempDir()
	SetDataDir(dir)

	if FluxorDataDir != dir {
		t.Fatalf("FluxorDataDir = %q, want %q", FluxorDataDir, dir)
	}
	for name, got := range map[string]string{
		"fluxor.pid":  FluxorPidFile,
		"core.pid":    CorePidFile,
		"fluxor.json": FluxorConfigFile,
		"fluxor.log":  FluxorLogFile,
	} {
		if want := filepath.Join(dir, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestDataDirByMode 校验两种模式的默认数据目录与 PID 目录：
// fnos 四个文件同址（TRIM_PKGVAR 可覆盖、缺失时回退），
// openwrt 的 PID 固定在 /var/run 且不受数据目录（含覆盖）影响。
func TestDataDirByMode(t *testing.T) {
	defer capturePaths()()

	t.Setenv("TRIM_PKGVAR", "/var/apps/Fluxor/var")
	SetDefaults("openwrt")
	if FluxorDataDir != "/etc/fluxor/" {
		t.Errorf("openwrt FluxorDataDir = %q, want /etc/fluxor/", FluxorDataDir)
	}
	if FluxorConfigFile != "/etc/fluxor/fluxor.json" || FluxorLogFile != "/etc/fluxor/fluxor.log" {
		t.Errorf("openwrt fluxor.json/fluxor.log 未落在 /etc/fluxor/：%q %q",
			FluxorConfigFile, FluxorLogFile)
	}
	if FluxorPidFile != "/var/run/fluxor.pid" || CorePidFile != "/var/run/core.pid" {
		t.Errorf("openwrt PID 未固定在 /var/run/：%q %q", FluxorPidFile, CorePidFile)
	}

	// openwrt 模式不受 TRIM_PKGVAR 影响
	t.Setenv("TRIM_PKGVAR", "/elsewhere")
	SetDefaults("openwrt")
	if FluxorDataDir != "/etc/fluxor/" {
		t.Errorf("openwrt 不应读取 TRIM_PKGVAR，得到 %q", FluxorDataDir)
	}

	// FLUXOR_DATA_DIR 覆盖只搬走 fluxor.json / fluxor.log，PID 仍在 /var/run
	SetDataDir("/tmp/openwrt-data")
	if FluxorConfigFile != "/tmp/openwrt-data/fluxor.json" || FluxorLogFile != "/tmp/openwrt-data/fluxor.log" {
		t.Errorf("覆盖数据目录后 fluxor.json/fluxor.log 未跟随：%q %q", FluxorConfigFile, FluxorLogFile)
	}
	if FluxorPidFile != "/var/run/fluxor.pid" || CorePidFile != "/var/run/core.pid" {
		t.Errorf("覆盖数据目录后 PID 不应改变：%q %q", FluxorPidFile, CorePidFile)
	}

	t.Setenv("TRIM_PKGVAR", "/custom/pkgvar")
	SetDefaults("fnos")
	if FluxorDataDir != "/custom/pkgvar" {
		t.Errorf("fnos FluxorDataDir = %q, want /custom/pkgvar", FluxorDataDir)
	}
	if FluxorPidFile != "/custom/pkgvar/fluxor.pid" || CorePidFile != "/custom/pkgvar/core.pid" {
		t.Errorf("fnos PID 应与数据目录同址：%q %q", FluxorPidFile, CorePidFile)
	}

	// 脱离飞牛应用框架启动（TRIM_PKGVAR 未注入）时回退到既有路径
	t.Setenv("TRIM_PKGVAR", "")
	SetDefaults("fnos")
	if FluxorDataDir != "/var/apps/Fluxor/var" {
		t.Errorf("fnos 回退 FluxorDataDir = %q, want /var/apps/Fluxor/var", FluxorDataDir)
	}
	if FluxorPidFile != "/var/apps/Fluxor/var/fluxor.pid" || CorePidFile != "/var/apps/Fluxor/var/core.pid" {
		t.Errorf("fnos 回退后 PID 应与数据目录同址：%q %q", FluxorPidFile, CorePidFile)
	}
}

// capturePaths 保存当前全部路径变量（含「PID 目录是否被模式固定」的标记），
// 返回的恢复函数在用例结束时还原，避免污染同包其它用例。
func capturePaths() func() {
	saved := []*string{
		&SocketPath, &BaseURL, &TcpAddr, &FluxorBinDir, &CoreBin, &CoreSocket,
		&MetaDir, &ZashDir, &ConfigTarget, &CoreWorkDir, &OriginalBaseURL,
		&FluxorDataDir, &FluxorPidFile, &CorePidFile, &FluxorLogFile,
		&FluxorConfigFile, &FluxorSettingsFile, &FluxorRulesFile, &FluxorTunnelsFile,
		&FluxorMetaFile, &FluxorTproxyFile,
		&pidDirPinned,
	}
	values := make([]string, len(saved))
	for i, p := range saved {
		values[i] = *p
	}
	return func() {
		for i, p := range saved {
			*p = values[i]
		}
	}
}
