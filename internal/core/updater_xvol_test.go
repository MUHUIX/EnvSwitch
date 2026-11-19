package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMoveOverCrossVolume 是热更新跨盘回归测试。
//
// 线上故障：更新包暂存在 %LOCALAPPDATA%（C:\），程序装在别的盘，moveOver 里的
// os.Rename 走 MoveFileEx 且不带 MOVEFILE_COPY_ALLOWED，报
// ERROR_NOT_SAME_DEVICE「The system cannot move the file to a different disk drive」，
// 表现为「放置新版本失败」。裸 rename 会失败正是本测试的前提，故先断言前提成立。
//
// 源用 t.TempDir()（TMP 所在卷），目标用本包目录（仓库所在卷）——
// 仓库恰好与 TMP 同卷的机器上跨卷前提不成立，跳过而非失败。
func TestMoveOverCrossVolume(t *testing.T) {
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "EnvSwitch.exe")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(wd, ".xvol-probe.exe")
	defer os.Remove(dst)
	defer os.Remove(dst + ".incoming")

	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("写 %s: %v", p, err)
		}
	}
	write(src, "NEW")

	// 前提：两路径确实跨卷，否则本测试无法覆盖目标分支。
	if err := os.Rename(src, dst); err == nil {
		t.Skip("仓库目录与 TMP 同卷，跨卷前提不成立，跳过")
	}
	write(src, "NEW")

	// 跨卷就位：内容正确，且不留暂存原件与 .incoming（句柄未关会导致 Windows 删不掉）。
	if err := moveOver(src, dst); err != nil {
		t.Fatalf("moveOver 跨卷失败: %v", err)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "NEW" {
		t.Fatalf("就位内容不对: %q %v", got, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("暂存原件未清理：复制目标时源句柄可能未在返回前关闭")
	}
	if _, err := os.Stat(dst + ".incoming"); !os.IsNotExist(err) {
		t.Fatal("临时文件未清理")
	}

	// 目标位已有旧文件时必须被覆盖（兄弟 CLI envs.exe 不让位的真实情形）。
	write(src, "NEW2")
	write(dst, "OLD-AND-LONGER")
	if err := moveOver(src, dst); err != nil {
		t.Fatalf("moveOver 覆盖已存在目标失败: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "NEW2" {
		t.Fatalf("旧文件未被正确覆盖: %q", got)
	}

	// 同卷快路径不能被破坏：源与目标同目录时仍应走 rename 而非复制。
	sameSrc := filepath.Join(wd, ".xvol-same-vol-src.exe")
	sameDst := filepath.Join(wd, ".xvol-same-vol-dst.exe")
	defer os.Remove(sameSrc)
	defer os.Remove(sameDst)
	write(sameSrc, "SAME")
	if err := moveOver(sameSrc, sameDst); err != nil {
		t.Fatalf("moveOver 同卷失败: %v", err)
	}
	if _, err := os.Stat(sameSrc); !os.IsNotExist(err) {
		t.Fatal("同卷应走 rename 移走源文件")
	}
	if got, _ := os.ReadFile(sameDst); string(got) != "SAME" {
		t.Fatalf("同卷就位内容不对: %q", got)
	}
}
