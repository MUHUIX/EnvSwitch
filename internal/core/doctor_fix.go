package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

// —— 体检项的一键修复与撤销 ——
//
// 修复只做 doctor 报事时已经给出过的「手动修法」，不引入新口径：
//  1. 只增删/重排 **EnvSwitch 自己写进用户 PATH 的条目**（空壳、来源不稳、被抢位），
//     绝不碰系统 PATH（需要管理员），也绝不摘不归 EnvSwitch 所有的条目；
//  2. 把失效的激活 junction 重指到本机已有版本、把漂移的托管变量校准到当前激活版本根
//     —— 二者都走既有的 Switch / SyncManagedEnv，等于替用户敲一遍 envs use。
//
// 每一次写用户 PATH 之前，先把原值快照到 home\doctor-fix.json（写盘失败就不动 PATH），
// UndoDoctorFix 一次还原。快照只保留最近一条：撤销 = 回到上一次修复之前，语义唯一。

// sysWriteUserPath / userPathCanWrite 留成变量：单测注入内存实现，
// 绝不在真实 HKCU\Environment 上试写，也不依赖测试机的注册表权限。
var (
	sysWriteUserPath   = sys.WriteUserPath
	userPathCanWrite   = sys.UserPathCanWrite
	broadcastEnvChange = sys.BroadcastEnvChange
)

// 支持一键修复的体检项名（与 Doctor 里的 Check.Name 严格一致）。
const (
	CheckPathPriority    = "PATH 优先级"
	CheckStalePathEntry  = "空壳 PATH 条目"
	CheckPathProvenance  = "PATH 条目来源"
	CheckJunctionLinks   = "激活链接"
	CheckManagedEnvLevel = "托管变量层级"
)

// fixableChecks 是「哪些体检项能自动修」的唯一清单。不支持修复的项一律不给按钮，
// 也不做半吊子写入：注册表不可写要改组策略、WebView2 缺失要装运行库、
// nvm/scoop 共存与环境规模只是事实陈述，都不该由 EnvSwitch 代用户决定。
var fixableChecks = map[string]bool{
	CheckPathPriority:    true,
	CheckStalePathEntry:  true,
	CheckPathProvenance:  true,
	CheckJunctionLinks:   true,
	CheckManagedEnvLevel: true,
}

// FixableCheck 报告某个体检项是否支持一键修复。
func FixableCheck(name string) bool { return fixableChecks[name] }

// FixableFor 在「这一类问题原则上可修」之上，再判一层「此刻这台机器上真修得了」：
// PATH 优先级如果全是系统段抢位，EnvSwitch 没有管理员权限、也不该动系统段，
// 那就不能给按钮 —— 给了只会让用户点了个寂寞。
func FixableFor(name string, ads []Adapter, s *settings.Settings) bool {
	if !FixableCheck(name) {
		return false
	}
	if name != CheckPathPriority {
		return true
	}
	userRaw, err := doctorUserPath()
	if err != nil {
		return false
	}
	_, hijacked := pathResolution(ads, s, doctorMachinePath(), userRaw)
	for _, h := range hijacked {
		if !strings.Contains(h.Origin, "系统") {
			return true
		}
	}
	return false
}

// FixableCheckNames 返回可修复项名（供 CLI 与提示文案复用同一清单）。
func FixableCheckNames() []string {
	out := make([]string, 0, len(fixableChecks))
	for k := range fixableChecks {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fixSnapshot 是写用户 PATH 前后的两份值。
// afterPath 必须一起存：撤销时要判断「修复之后、撤销之前有没有第三方又改过 PATH」，
// 拿 cur 跟 beforePath 比是错的 —— 那两者按定义就不相等，每次都会误报。
type fixSnapshot struct {
	SavedAt   string   `json:"savedAt"`
	UserPath  string   `json:"userPath"` // 修复前的原值（撤销的目标）
	AfterPath string   `json:"afterPath"`
	Actions   []string `json:"actions"`
}

// DoctorFix 修复体检项；name 为空 = 修复当前所有可修且仍异常的项。
// 返回逐项结论（含「已复检通过」或「改不了，为什么」的如实说明）。
func DoctorFix(s *settings.Settings, ads []Adapter, name string) ([]string, error) {
	names := []string{name}
	if name == "" {
		names = nil
		for _, c := range Doctor(ads, s) {
			if c.Status != "warn" && c.Status != "bad" {
				continue
			}
			// 用 FixableFor 而不是 FixableCheck：此刻修不了的（系统段抢位）不进批次，
			// 免得 --fix 白写一次快照再回一句「改不了」。
			if FixableFor(c.Name, ads, s) {
				names = append(names, c.Name)
			}
		}
		if len(names) == 0 {
			return []string{"没有需要修复的异常项"}, nil
		}
	}
	var notes []string
	for _, n := range names {
		if !FixableCheck(n) {
			return notes, W(ErrUsage, "「%s」不支持一键修复，doctor 的说明里给了手动处理办法", n)
		}
		got, err := fixOne(s, ads, n)
		notes = append(notes, got...)
		if err != nil {
			return notes, err
		}
	}
	return notes, nil
}

func fixOne(s *settings.Settings, ads []Adapter, name string) ([]string, error) {
	userRaw, err := doctorUserPath()
	if err != nil {
		return nil, W(ErrPerm, "读取用户 PATH 失败：%v", err)
	}
	switch name {
	case CheckPathPriority:
		return fixPathPriority(s, ads, userRaw)
	case CheckStalePathEntry:
		return fixDropEntries(s, userRaw, name)
	case CheckPathProvenance:
		return fixDropEntries(s, userRaw, name)
	case CheckJunctionLinks:
		return fixDeadJunctions(s, ads)
	case CheckManagedEnvLevel:
		return fixManagedEnv(s, ads)
	}
	return nil, W(ErrUsage, "未知体检项 %q", name)
}

// —— 用户 PATH 的三类修复 ——

// dropTargets 给出某一体检项要摘掉的原始条目（展开 %VAR% 后的形态）。
func dropTargets(name, userRaw string) []string {
	switch name {
	case CheckStalePathEntry:
		ps, _ := staleOwnedPaths(userRaw)
		return ps
	case CheckPathProvenance:
		ps, _ := unstableOwnedPaths(userRaw)
		return ps
	}
	return nil
}

// fixDropEntries 摘掉体检点名的 EnvSwitch 自有条目，然后复检。
func fixDropEntries(s *settings.Settings, userRaw, name string) ([]string, error) {
	targets := dropTargets(name, userRaw)
	if len(targets) == 0 {
		return []string{fmt.Sprintf("%s：已经没有问题条目，无需修复", name)}, nil
	}
	keep := map[string]bool{}
	for _, t := range inUseOwnedEntries(s) {
		keep[pathKey(t)] = true
	}
	drop := map[string]bool{}
	var skipped []string
	for _, t := range targets {
		// 仍在服役的自有条目（例如整套路径都在 git 工作区里的开发机）不摘：
		// 摘掉它等于把 EnvSwitch 自己的激活链路拆了，比它报的那个 warn 严重得多。
		if keep[pathKey(t)] {
			skipped = append(skipped, t)
			continue
		}
		drop[pathKey(t)] = true
	}
	if len(drop) == 0 {
		return []string{fmt.Sprintf("%s：点名的 %d 条仍是 EnvSwitch 正在使用的路径，跳过未摘（要清它请把版本库/安装目录搬到正式位置，或先 envs use 重指）：%s",
			name, len(skipped), strings.Join(skipped, "；"))}, nil
	}
	parts := splitUserPath(userRaw)
	rest := make([]string, 0, len(parts))
	var removed []string
	for _, p := range parts {
		if drop[pathKey(p)] || drop[pathKey(expandPercent(p))] {
			removed = append(removed, p)
			continue
		}
		rest = append(rest, p)
	}
	next := strings.Join(rest, ";")
	if err := writeFixedUserPath(s, userRaw, next, []string{name}); err != nil {
		return nil, err
	}
	notes := []string{fmt.Sprintf("%s：已从用户 PATH 摘掉 %d 条失效条目（%s）", name, len(removed), strings.Join(removed, " ; "))}
	if again := dropTargets(name, next); len(again) > 0 {
		notes = append(notes, fmt.Sprintf("复检：仍有 %d 条同类条目未清（%s）", len(again), strings.Join(again, " ; ")))
	} else {
		notes = append(notes, "复检：同类条目已清零 ✓")
	}
	if len(skipped) > 0 {
		notes = append(notes, fmt.Sprintf("另有 %d 条仍是 EnvSwitch 正在使用的路径，已跳过未摘：%s", len(skipped), strings.Join(skipped, "；")))
	}
	return notes, nil
}

// fixPathPriority 把 EnvSwitch 自有五条路径整体提到用户 PATH 最前（EnsureUserPath 的口径），
// 解掉「用户 PATH 段里被别家目录抢先」。系统 PATH 的抢位修不了：那要管理员，EnvSwitch 不动它。
func fixPathPriority(s *settings.Settings, ads []Adapter, userRaw string) ([]string, error) {
	_, hijacked := pathResolution(ads, s, doctorMachinePath(), userRaw)
	if len(hijacked) == 0 {
		return []string{CheckPathPriority + "：按当前 PATH 全表复检，已激活的 runtime 都落在 EnvSwitch 目录，无需修复"}, nil
	}
	var sysSide, userSide []string
	for _, h := range hijacked {
		if strings.Contains(h.Origin, "系统") {
			sysSide = append(sysSide, fmt.Sprintf("%s←%s", h.Tool, h.Winner))
		} else {
			userSide = append(userSide, fmt.Sprintf("%s←%s", h.Tool, h.Winner))
		}
	}
	var notes []string
	if len(userSide) == 0 {
		return []string{fmt.Sprintf("%s：抢位来自系统 PATH（%s），EnvSwitch 无管理员权限不动它，需手动在「编辑系统环境变量」里处理；目录绑定不受影响（走会话注入）",
			CheckPathPriority, strings.Join(sysSide, ", "))}, nil
	}
	owned := settings.OwnedPathEntries(s)
	next := strings.Join(reorderToFront(splitUserPath(userRaw), owned), ";")
	if next == userRaw {
		return []string{fmt.Sprintf("%s：EnvSwitch 条目已经在用户 PATH 最前，抢位者不靠顺序压制（%s），需手动删除那几条路径",
			CheckPathPriority, strings.Join(userSide, ", "))}, nil
	}
	if err := writeFixedUserPath(s, userRaw, next, []string{CheckPathPriority}); err != nil {
		return nil, err
	}
	notes = append(notes, fmt.Sprintf("%s：已把 %d 条 EnvSwitch 路径提到用户 PATH 最前（%s）",
		CheckPathPriority, len(owned), strings.Join(owned, " ; ")))
	_, still := pathResolution(ads, s, doctorMachinePath(), next)
	if len(still) == 0 {
		notes = append(notes, "复检：新开终端按全表解析，已激活的 runtime 全部落在 EnvSwitch 目录 ✓")
		return notes, nil
	}
	var left []string
	for _, h := range still {
		left = append(left, fmt.Sprintf("%s←%s（%s）", h.Tool, h.Winner, h.Origin))
	}
	notes = append(notes, "复检：仍有抢位未解（"+strings.Join(left, ", ")+
		"）—— 系统 PATH 段永远排在用户段之前，EnvSwitch 无管理员权限不动它，需在「编辑系统环境变量」里删掉那几条")
	return notes, nil
}

// —— 激活链接与托管变量 ——

// fixDeadJunctions 把失效的 current\<tool> 链接重指到本机已存在的最新版本
// （等价于 doctor 提示的「重新 use 即可修复」）。没有任何可版本时不猜，如实报。
func fixDeadJunctions(s *settings.Settings, ads []Adapter) ([]string, error) {
	_, paths, tools := junctionHealth(ads, s, settings.OwnedPathEntries(s))
	if len(paths) == 0 {
		return []string{CheckJunctionLinks + "：激活链接全部有效，无需修复"}, nil
	}
	var notes []string
	for _, tool := range tools {
		ad := adapterByID(ads, tool)
		if ad == nil {
			notes = append(notes, fmt.Sprintf("%s：找不到适配器，跳过", tool))
			continue
		}
		installs := ListInstalls(ad, s)
		if len(installs) == 0 {
			notes = append(notes, fmt.Sprintf("%s：链接失效且本机没有任何可用版本，无法重建（先安装一个版本，或取消该工具的激活）", tool))
			continue
		}
		if err := Switch(s, ad, &installs[0]); err != nil {
			notes = append(notes, fmt.Sprintf("%s：重指到 %s 失败 —— %v", tool, installs[0].Version, err))
			continue
		}
		link := settings.CurrentLink(s, tool)
		if _, ok := junctionTarget(link); !ok {
			notes = append(notes, fmt.Sprintf("%s：重指后复检仍无效（%s → %s）", tool, link, installs[0].BinDir))
			continue
		}
		notes = append(notes, fmt.Sprintf("%s：已重指到本机最新的 %s %s（%s）✓", tool, tool, installs[0].Version, installs[0].BinDir))
	}
	if dropped, err := SyncManagedEnv(s, ads); err != nil {
		notes = append(notes, fmt.Sprintf("托管变量同步失败：%v", err))
	} else if len(dropped) > 0 {
		notes = append(notes, "已取消托管并恢复原值："+strings.Join(dropped, ", "))
	}
	return notes, nil
}

// fixManagedEnv 把漂移的托管变量（JAVA_HOME/GOROOT 等）校准回当前激活版本根。
// 这是「写正确的值」，撤销它等于把环境改回错的状态，所以不进 PATH 快照、不可撤销。
func fixManagedEnv(s *settings.Settings, ads []Adapter) ([]string, error) {
	dropped, err := SyncManagedEnv(s, ads)
	if err != nil {
		return nil, fmt.Errorf("校准托管变量失败: %w", err)
	}
	var notes []string
	if len(dropped) > 0 {
		notes = append(notes, "目标工具已无激活版本，已恢复原值并取消托管："+strings.Join(dropped, ", "))
	}
	if cs := managedEnvChecks(ads, s); len(cs) > 0 && cs[0].Status == "ok" {
		notes = append(notes, CheckManagedEnvLevel+"：托管变量已校准到当前激活版本根，复检通过 ✓")
	} else if len(dropped) == 0 {
		notes = append(notes, CheckManagedEnvLevel+"：没有可校准的托管项（可能该项需要重新 use 才会写入）")
	}
	return notes, nil
}

// —— 快照 / 撤销 ——

// writeFixedUserPath 先落快照再写 PATH：快照没写成，就一个字节都不动注册表，
// 绝不留下「改了环境但撤不回」的状态。
func writeFixedUserPath(s *settings.Settings, oldRaw, newRaw string, actions []string) error {
	if os.Getenv("ENVS_NO_PATH") == "1" {
		return W(ErrUsage, "ENVS_NO_PATH=1（隔离模式）：不写用户 PATH")
	}
	if !userPathCanWrite() {
		return W(ErrPerm, `HKCU\Environment 不可写，无法修复（检查组策略后重试，或按 doctor 说明手动处理）`)
	}
	if err := saveFixSnapshot(s, oldRaw, newRaw, actions); err != nil {
		return fmt.Errorf("写修复快照失败，已放弃修复: %w", err)
	}
	if err := sysWriteUserPath(newRaw); err != nil {
		return fmt.Errorf("写用户 PATH 失败: %w", err)
	}
	broadcastEnvChange()
	return nil
}

func saveFixSnapshot(s *settings.Settings, oldRaw, newRaw string, actions []string) error {
	data, err := json.MarshalIndent(fixSnapshot{
		SavedAt:   time.Now().Format(time.RFC3339),
		UserPath:  oldRaw,
		AfterPath: newRaw,
		Actions:   actions,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(settings.Home(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(settings.DoctorFixBackup(s), data, 0o644)
}

// UndoDoctorFix 把用户 PATH 还原到上一次修复之前。只能撤一步（快照只留最近一条）。
func UndoDoctorFix(s *settings.Settings) ([]string, error) {
	data, err := os.ReadFile(settings.DoctorFixBackup(s))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, W(ErrNotFound, "没有可撤销的修复快照")
		}
		return nil, err
	}
	var snap fixSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("修复快照已损坏，拒绝自动还原（可在「编辑账户的环境变量」里手动核对）: %w", err)
	}
	if !userPathCanWrite() {
		return nil, W(ErrPerm, `HKCU\Environment 不可写，无法撤销`)
	}
	cur, _ := doctorUserPath()
	if err := sysWriteUserPath(snap.UserPath); err != nil {
		return nil, fmt.Errorf("还原用户 PATH 失败: %w", err)
	}
	_ = os.Remove(settings.DoctorFixBackup(s))
	broadcastEnvChange()
	act := strings.Join(snap.Actions, "、")
	if act == "" {
		act = "PATH 调整"
	}
	notes := []string{fmt.Sprintf("已撤销「%s」（%s 的修复），用户 PATH 还原到修复前的值", act, snap.SavedAt)}
	// 只有和「修复后应是的值」比对，才判得出中间有没有第三方插过 PATH。
	// 老快照没有 afterPath（v1.6.0 之前写的），无从判断，就不给这句提示。
	if snap.AfterPath != "" && cur != snap.AfterPath {
		notes = append(notes, "注意：撤销前读到的用户 PATH 与本次修复写入的值不同（中间还有别的改动），这次撤销把它整体回滚到了修复前")
	}
	return notes, nil
}

// DoctorFixState 报告当前是否有一次可撤销的 PATH 修复。
func DoctorFixState(s *settings.Settings) (canUndo bool, savedAt string, actions []string) {
	data, err := os.ReadFile(settings.DoctorFixBackup(s))
	if err != nil {
		return false, "", nil
	}
	var snap fixSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return false, "", nil
	}
	return true, snap.SavedAt, snap.Actions
}

// —— 小工具 ——

func splitUserPath(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// pathKey 用于比较同一条路径：展开 %VAR%、去尾分隔符、统一小写。
func pathKey(p string) string {
	return strings.ToLower(filepath.Clean(expandPercent(strings.TrimSpace(p))))
}

// reorderToFront 把 owned 按给定顺序提到最前，其余条目保持原相对顺序（去重）。
func reorderToFront(parts, owned []string) []string {
	set := map[string]bool{}
	for _, o := range owned {
		set[pathKey(o)] = true
	}
	rest := make([]string, 0, len(parts))
	for _, p := range parts {
		if !set[pathKey(p)] {
			rest = append(rest, p)
		}
	}
	return append(append([]string{}, owned...), rest...)
}

// inUseOwnedEntries 返回「现在还在服役」的 EnvSwitch 路径：条目本身存在且不是空壳。
// fixDropEntries 拒绝摘掉它们 —— 摘了比原来的 warn 更糟（激活链路直接断）。
func inUseOwnedEntries(s *settings.Settings) []string {
	var out []string
	for _, o := range settings.OwnedPathEntries(s) {
		if _, ok := junctionTarget(o); ok {
			out = append(out, o) // 有效激活链接
			continue
		}
		fi, err := os.Stat(o)
		if err != nil || !fi.IsDir() {
			continue // 目录都不在：正是该清的空壳
		}
		if names, err := os.ReadDir(o); err == nil && len(names) > 0 {
			out = append(out, o) // 有内容的 shims / 安装目录
		}
	}
	return out
}
