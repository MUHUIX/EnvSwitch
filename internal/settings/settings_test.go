package settings

import (
	"os"
	"path/filepath"
	"strings"
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

// §9-2 / §2-L2：shims 必须排在 current\node 之前 —— node 版本目录自带 npm.cmd，
// 顺序在后面就等于 EnvSwitch 自己的 npm shim 永远不会被执行，层 2 形同不存在。
func TestOwnedPathEntriesShimsFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &Settings{}
	got := OwnedPathEntries(s)
	if len(got) != 5 {
		t.Fatalf("PATH 自有条目应是 5 条（方案 §1 纠正①），实得 %d: %v", len(got), got)
	}
	if got[0] != ShimsDir(s) {
		t.Fatalf("第 1 条必须是 shims 目录，实得 %q（应为 %q）", got[0], ShimsDir(s))
	}
	want := []string{ShimsDir(s), CurrentLink(s, "node"), CurrentLink(s, "python"), CurrentLink(s, "go")}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("第 %d 条顺序不对：期望 %q，实得 %q", i+1, w, got[i])
		}
	}
	if got[4] != ExeDir() {
		t.Fatalf("最后一条应是安装目录，实得 %q", got[4])
	}
}

// 幂等前提：条目集合确定、无重复、每条都被 IsOwnedPath 认出来。
// 这三条破了，EnsureUserPath「先摘旧条目再整体前插」就会反复判定有变化 → path ensure 每次写注册表。
// （真实的注册表往返不在本测试范围内：本任务硬约束禁止写 HKCU，EnsureUserPath 本身未改。）
func TestOwnedPathEntriesIdempotentPreconditions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &Settings{}
	first := OwnedPathEntries(s)
	second := OwnedPathEntries(s)
	if strings.Join(first, "|") != strings.Join(second, "|") {
		t.Fatalf("两次调用结果不一致，path ensure 会反复追加:\n%v\n%v", first, second)
	}
	seen := map[string]bool{}
	for _, p := range first {
		k := strings.ToLower(filepath.Clean(p))
		if seen[k] {
			t.Fatalf("条目重复：%q", p)
		}
		seen[k] = true
		if !IsOwnedPath(p) {
			t.Fatalf("IsOwnedPath 不认自己写的条目 %q —— path restore 会把它留在用户 PATH 上", p)
		}
	}
}
