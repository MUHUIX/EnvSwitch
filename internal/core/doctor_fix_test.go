package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MUHUIX/envswitch/internal/settings"
)

// 一键修复的分支覆盖：判定与 Doctor 同源，所以这里重点验「改完之后是不是真的好了」
// 以及「撤销能不能回到改之前」。全部走注入桩：绝不读写真实 HKCU\Environment。

// stubUserPathWrite 把用户 PATH 的读写换成内存变量，返回当前的读取函数与改写函数
// （改写函数用于模拟「修复之后、撤销之前，第三方又动了 PATH」）。
func stubUserPathWrite(t *testing.T, initial string) (func() string, func(string)) {
	t.Helper()
	cur := initial
	om, ou, ow, oc, ob := doctorMachinePath, doctorUserPath, sysWriteUserPath, userPathCanWrite, broadcastEnvChange
	doctorUserPath = func() (string, error) { return cur, nil }
	sysWriteUserPath = func(v string) error { cur = v; return nil }
	userPathCanWrite = func() bool { return true }
	broadcastEnvChange = func() {}
	t.Cleanup(func() {
		doctorMachinePath, doctorUserPath, sysWriteUserPath, userPathCanWrite, broadcastEnvChange = om, ou, ow, oc, ob
	})
	return func() string { return cur }, func(v string) { cur = v }
}

func TestFixableCheckList(t *testing.T) {
	for _, n := range []string{"PATH 优先级", "空壳 PATH 条目", "PATH 条目来源", "激活链接", "托管变量层级"} {
		if !FixableCheck(n) {
			t.Errorf("%s 应支持修复", n)
		}
	}
	for _, n := range []string{"注册表写入", "WebView2", "nvm-windows 共存", "版本库占用", "开机自启", "层 2 兜底"} {
		if FixableCheck(n) {
			t.Errorf("%s 不该被标成可修复（要么需要管理员、要么只是事实陈述）", n)
		}
	}
}

// 空壳条目：摘掉报过的那几条，且撤销能整体还原；仍在服役的自有路径一条都不许摘。
func TestFixStalePathEntriesAndUndo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}

	// go 是普通空目录（壳）、rust 的链接目录已不存在（孤儿）：两条都该被摘；
	// node 是有效 junction（在服役）：绝不能被摘。
	goShell := settings.CurrentLink(s, "go")
	if err := os.MkdirAll(goShell, 0o755); err != nil {
		t.Fatal(err)
	}
	rustDead := settings.CurrentLink(s, "rust")
	ours := touchExe(t, filepath.Join(home, "store", "node", "22.22.0"), "node.exe")
	junctionTo(t, s, "node", ours)

	user := strings.Join([]string{`C:\Windows`, goShell, rustDead, settings.CurrentLink(s, "node")}, ";")
	read, _ := stubUserPathWrite(t, user)
	t.Cleanup(func() { _ = os.Remove(settings.DoctorFixBackup(s)) })

	ads := []Adapter{docAdapter{id: "go", exe: "go.exe", binSub: "bin"}, docAdapter{id: "node", exe: "node.exe"}}
	// 前置事实：doctor 确实报了这两条
	if got := staleOwnedPathEntries(user); len(got) != 2 {
		t.Fatalf("构造的空壳场景应报 2 条，实得 %v", got)
	}

	notes, err := DoctorFix(s, ads, CheckStalePathEntry)
	if err != nil {
		t.Fatalf("修复失败: %v（%v）", err, notes)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "摘掉 2 条") || !strings.Contains(joined, "复检") {
		t.Fatalf("修复结论不对：%v", notes)
	}
	next := read()
	if strings.Contains(next, goShell) || strings.Contains(next, rustDead) {
		t.Fatalf("空壳条目没摘干净：%s", next)
	}
	if !strings.Contains(next, `C:\Windows`) || !strings.Contains(next, settings.CurrentLink(s, "node")) {
		t.Fatalf("误伤了别人的/在服役的条目：%s", next)
	}
	if again := staleOwnedPathEntries(next); len(again) != 0 {
		t.Fatalf("复检应清零：%v", again)
	}
	if can, at, acts := DoctorFixState(s); !can || at == "" || len(acts) == 0 {
		t.Fatalf("修复后应有可撤销快照：%v %q %v", can, at, acts)
	}

	// 撤销：整串回到修复前的值，快照文件消失
	undone, err := UndoDoctorFix(s)
	if err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	if len(undone) == 0 {
		t.Fatal("撤销要给结论")
	}
	// 没有第三方插手时不该冒出「中间还有别的改动」：比对基准是修复后写入的值
	if strings.Contains(strings.Join(undone, "\n"), "中间还有别的改动") {
		t.Fatalf("撤销误报中间有改动：%v", undone)
	}
	if read() != user {
		t.Fatalf("撤销后 PATH 应回到原值：\n got=%s\nwant=%s", read(), user)
	}
	if can, _, _ := DoctorFixState(s); can {
		t.Fatal("撤销后不应再有余量可撤")
	}
	if _, err := UndoDoctorFix(s); err == nil {
		t.Fatal("没有快照时撤销应报错，而不是静默成功")
	}
}

// 用户段被抢位：把 EnvSwitch 自有条目提到最前，复检后不再被抢。
func TestFixPathPriorityReordersAndVerifies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	ours := touchExe(t, filepath.Join(home, "store", "node", "22.22.0"), "node.exe")
	junctionTo(t, s, "node", ours)
	// 抢位者在**用户段**且排在 EnvSwitch 之前 —— 这种才修得了
	other := touchExe(t, filepath.Join(t.TempDir(), "node"), "node.exe")

	user := other + ";" + strings.Join(settings.OwnedPathEntries(s), ";")
	read, _ := stubUserPathWrite(t, user)
	stubDoctorMachine(t, `C:\Windows\system32`)
	t.Cleanup(func() { _ = os.Remove(settings.DoctorFixBackup(s)) })

	ads := []Adapter{docAdapter{id: "node", exe: "node.exe"}}
	if _, hijacked := pathResolution(ads, s, `C:\Windows\system32`, user); len(hijacked) != 1 {
		t.Fatalf("构造的抢位场景未被识别：%+v", hijacked)
	}
	notes, err := DoctorFix(s, ads, CheckPathPriority)
	if err != nil {
		t.Fatalf("修复失败: %v（%v）", err, notes)
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "复检") || !strings.Contains(joined, "✓") {
		t.Fatalf("修复结论应包含复检通过：%v", notes)
	}
	next := read()
	if _, hijacked := pathResolution(ads, s, `C:\Windows\system32`, next); len(hijacked) != 0 {
		t.Fatalf("修复后仍被抢：%+v", hijacked)
	}
	if !strings.HasPrefix(next, settings.ShimsDir(s)) {
		t.Fatalf("EnvSwitch 条目没有提到最前：%s", next)
	}
	if !strings.Contains(next, other) {
		t.Fatalf("抢位目录应保留（只是排到后面），不该被删：%s", next)
	}
}

// 系统段抢位：明确说修不了，且不写 PATH。
func TestFixPathPriorityRefusesSystemHijack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	ours := touchExe(t, filepath.Join(home, "store", "node", "22.22.0"), "node.exe")
	junctionTo(t, s, "node", ours)
	sysDir := touchExe(t, filepath.Join(t.TempDir(), "nvm4w", "nodejs"), "node.exe")

	user := strings.Join(settings.OwnedPathEntries(s), ";")
	read, _ := stubUserPathWrite(t, user)
	stubDoctorMachine(t, sysDir+`;C:\Windows\system32`)

	notes, err := DoctorFix(s, []Adapter{docAdapter{id: "node", exe: "node.exe"}}, CheckPathPriority)
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "系统 PATH") {
		t.Fatalf("要如实说明抢位来自系统 PATH、修不了：%v", notes)
	}
	if read() != user {
		t.Fatal("修不了就不该动用户 PATH")
	}
	if _, err := os.Stat(settings.DoctorFixBackup(s)); err == nil {
		t.Fatal("没写入就不该留下快照")
	}
}

// 失效激活链接：重指到本机最新版本，doctor 随之转绿。
// 用 go（doctor 监控的三个激活链接之一：node/python/go）而不是任意工具。
func TestFixDeadJunctionRepointsToNewest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{EnvBackup: map[string]settings.EnvBackup{}}

	// 两个 go 版本（BinSub=bin），链接指向一个已被删掉的目录 → 死链
	old := touchExe(t, filepath.Join(home, "store", "go", "1.22.0", "bin"), "go.exe")
	touchExe(t, filepath.Join(home, "store", "go", "1.26.5", "bin"), "go.exe")
	link := junctionTo(t, s, "go", old)
	if err := os.RemoveAll(filepath.Join(home, "store", "go", "1.22.0")); err != nil {
		t.Fatal(err)
	}
	if _, ok := junctionTarget(link); ok {
		t.Skip("本机 junction 指向已删目录仍可解析，无法构造死链场景")
	}
	read, _ := stubUserPathWrite(t, strings.Join(settings.OwnedPathEntries(s), ";"))
	stubDoctorMachine(t, `C:\Windows\system32`)
	t.Cleanup(func() { _ = os.Remove(settings.DoctorFixBackup(s)) })

	ads := []Adapter{docAdapter{id: "go", exe: "go.exe", binSub: "bin"}}
	_, deadPaths, deadTools := junctionHealth(ads, s, settings.OwnedPathEntries(s))
	if len(deadPaths) != 1 || deadTools[0] != "go" {
		t.Fatalf("构造的死链场景未被识别：%v / %v", deadPaths, deadTools)
	}
	notes, err := DoctorFix(s, ads, CheckJunctionLinks)
	if err != nil {
		t.Fatalf("修复失败: %v（%v）", err, notes)
	}
	tgt, ok := junctionTarget(link)
	if !ok {
		t.Fatalf("链接仍失效：%s（%v）", link, notes)
	}
	if filepath.Base(filepath.Dir(tgt)) != "1.26.5" {
		t.Fatalf("应重指到本机最新版本 1.26.5，实得 %s", tgt)
	}
	// 复检：doctor 的激活链接项应回到 ok
	alive, deadAfter, _ := junctionHealth(ads, s, settings.OwnedPathEntries(s))
	if len(deadAfter) != 0 {
		t.Fatalf("修复后仍有死链：%v（alive=%d）", deadAfter, alive)
	}
	if read() != strings.Join(settings.OwnedPathEntries(s), ";") {
		t.Fatal("重指链接不该动用户 PATH")
	}
}

// 托管变量漂走（值指到别处 / 多了一级 \bin）：修复校准回当前激活版本根并复检通过。
func TestFixManagedEnvRecalibrates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	env := stubUserEnv(t)
	stubUserPathWrite(t, "")
	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	binDir := touchExe(t, filepath.Join(home, "store", "java", "17.0.9", "bin"), "java.exe")
	junctionTo(t, s, "java", binDir)
	s.ManagedEnv = []string{"JAVA_HOME"}
	s.EnvBackup["JAVA_HOME"] = settings.EnvBackup{Had: true, Value: `C:\OldJdk`}
	env["JAVA_HOME"] = binDir // 老 bug 的形状：托管值多了一级 \bin

	notes, err := DoctorFix(s, []Adapter{docAdapter{id: "java", exe: "java.exe", binSub: "bin"}}, CheckManagedEnvLevel)
	if err != nil {
		t.Fatalf("修复失败: %v（%v）", err, notes)
	}
	if want := filepath.Join(home, "store", "java", "17.0.9"); !strings.EqualFold(env["JAVA_HOME"], want) {
		t.Fatalf("托管值应校准到版本根 %s，实得 %s", want, env["JAVA_HOME"])
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "✓") {
		t.Fatalf("修复结论应包含复检通过：%v", notes)
	}
}

// FixableFor 区分「用户段抢位（修得了）」与「系统段抢位（修不了）」：
// 真机上 PATH 优先级报 bad 时，只有前者该出修复按钮。
func TestFixableForOnlyWhenUserSideHijack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	touchExe(t, filepath.Join(home, "store", "node", "22.22.0"), "node.exe")
	junctionTo(t, s, "node", filepath.Join(home, "store", "node", "22.22.0"))
	userEntry := touchExe(t, filepath.Join(t.TempDir(), "u", "node"), "node.exe")
	sysEntry := touchExe(t, filepath.Join(t.TempDir(), "s", "node"), "node.exe")
	ads := []Adapter{docAdapter{id: "node", exe: "node.exe"}}

	stubUserPathWrite(t, userEntry+";"+strings.Join(settings.OwnedPathEntries(s), ";"))
	stubDoctorMachine(t, `C:\Windows\system32`)
	if !FixableFor(CheckPathPriority, ads, s) {
		t.Fatal("用户段抢位应标为可修复")
	}
	stubDoctorMachine(t, sysEntry+`;C:\Windows\system32`)
	if FixableFor(CheckPathPriority, ads, s) {
		t.Fatal("系统段抢位不该标可修复（无管理员权限，点了也修不了）")
	}
	if FixableFor("WebView2", ads, s) || FixableFor("注册表写入", ads, s) {
		t.Fatal("白名单外的项一律不可修")
	}
}

// 修复之后、撤销之前，第三方也改了 PATH：撤销要把这一点如实说出来。
func TestUndoWarnsWhenPathChangedInBetween(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	s := &settings.Settings{}
	shell := settings.CurrentLink(s, "go")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	user := `C:\Windows;` + shell
	read, setPath := stubUserPathWrite(t, user)
	t.Cleanup(func() { _ = os.Remove(settings.DoctorFixBackup(s)) })

	ads := []Adapter{docAdapter{id: "go", exe: "go.exe", binSub: "bin"}}
	if _, err := DoctorFix(s, ads, CheckStalePathEntry); err != nil {
		t.Fatalf("修复失败: %v", err)
	}
	setPath(read() + `;D:\someone-else`) // 模拟第三方插入

	notes, err := UndoDoctorFix(s)
	if err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "中间还有别的改动") {
		t.Fatalf("撤销应提示中间还有别的改动：%v", notes)
	}
	if read() != user {
		t.Fatalf("撤销应整体回滚到修复前的值：\n got=%s\nwant=%s", read(), user)
	}
}

func stubDoctorMachine(t *testing.T, machine string) {
	t.Helper()
	om := doctorMachinePath
	doctorMachinePath = func() string { return machine }
	t.Cleanup(func() { doctorMachinePath = om })
}

// ENVS_NO_PATH=1（隔离测试模式）下绝不写注册表，也不留快照。
func TestFixHonoursNoPathSwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVS_HOME", home)
	t.Setenv("ENVS_NO_PATH", "1")
	s := &settings.Settings{}
	shell := settings.CurrentLink(s, "go")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	read, _ := stubUserPathWrite(t, `C:\Windows;`+shell)
	_, err := DoctorFix(s, []Adapter{docAdapter{id: "go", exe: "go.exe", binSub: "bin"}}, CheckStalePathEntry)
	if err == nil {
		t.Fatal("隔离模式下应拒绝写 PATH")
	}
	if read() != `C:\Windows;`+shell {
		t.Fatal("隔离模式下不该改动 PATH")
	}
}
