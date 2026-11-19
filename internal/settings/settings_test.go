package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ENVS_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 旧配置只有 checkUpdateOnStart，且用户明确关过：迁移后不能被默认值 true 覆盖。
func TestLoadLegacyCheckUpdateOff(t *testing.T) {
	writeSettings(t, `{"version":1,"checkUpdateOnStart":false}`)
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoCheckUpdate {
		t.Fatal("旧配置 checkUpdateOnStart=false 应迁移为自动检查关闭")
	}
	if s.CheckUpdateOnStart {
		t.Fatal("CheckUpdateOnStart 应与 AutoCheckUpdate 保持一致")
	}
}

// 新配置显式写了 autoCheckUpdate=false，即使旧字段为 true 也以新字段为准。
func TestLoadExplicitAutoCheckWins(t *testing.T) {
	writeSettings(t, `{"version":1,"autoCheckUpdate":false,"checkUpdateOnStart":true}`)
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoCheckUpdate || s.CheckUpdateOnStart {
		t.Fatal("显式 autoCheckUpdate=false 不应被旧字段覆盖")
	}
}

// 全新安装（无配置文件）默认开启自动检查。
func TestLoadDefaultAutoCheckOn(t *testing.T) {
	t.Setenv("ENVS_HOME", t.TempDir())
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !s.AutoCheckUpdate || !s.CheckUpdateOnStart {
		t.Fatal("全新配置应默认开启自动检查更新")
	}
}
