package core

// 目录级版本绑定（bindings）内核 —— 方案《目录级版本绑定-最终方案-20261119-172026》
// §4 数据模型与解析算法、§5 工具支持矩阵判定、§10 托底矩阵的唯一实现。
// CLI / GUI / shim / shell 钩子四个入口都只能经由本文件取结果，禁止各自再解析一遍
// bindings.json（方案 §3「数据面只有一份真相」）。
//
// 条目对照：
//   §4.1 独立文件 %LOCALAPPDATA%\EnvSwitch\bindings.json，不进 settings.json
//   §4.2 路径归一（顺序敏感）+ cmpKey 唯一参与匹配
//   §4.3 最长前缀 + 分隔符边界 + 逐工具独立回退 + 表达式只认 16/16.20.2/latest + 白名单断言
//   §4.4 目录级 env 覆盖：只在会话 / envs run 内注入，绝不写注册表
//   §4.5 写 = .tmp+Rename + bindings.lock；读 = 无锁；损坏自动隔离 bindings.bad-<ts>
//   §5   执行层白名单硬编码（档①）；档③原生让位；档⑤直接拒绝
//   §10  任何异常都不阻塞命令：解析不出来就回落全局并留 warn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MUHUIX/envswitch/internal/settings"
)

// —— §4.1 数据模型 ————————————————————————————————

// BindingsFormatVersion 绑定表文件格式版本（当前只有 1）。
const BindingsFormatVersion = 1

// DefaultBindMaxDepth 是 walk-up 层数上限（§4.1 maxDepth）。
const DefaultBindMaxDepth = 24

// BindingsFileName / GenTableName 数据目录下的两个产物名。
// bindings.gen.txt 是给纯 bat / PS / sh 钩子读的扁平表，格式固定（§4.5 与 agent-A 的接口），
// 任何一列的顺序、分隔符、首行标记都不得改动。
const (
	BindingsFileName = "bindings.json"
	BindingsLockName = "bindings.lock"
	GenTableName     = "bindings.gen.txt"
	genTableHeader   = "#version=1"
	genColSep        = "\t"
	genColCount      = 4
	genEnvPairSep    = "&" // 契约 v2：多对之间用 & 分（编码域内不可能出现裸 &）

	// BindResolveHiddenCmd 是供 cmd AutoRun / shim 调用的隐藏命令名（§7.1）。
	// 它必须跳过 EnsureShims，且主路径（层 1 扁平面 / 层 3 envs run）都不依赖它。
	BindResolveHiddenCmd = "__bind-resolve"
)

// Binding 一条目录绑定（§4.1 bindings[]）。
type Binding struct {
	Path      string            `json:"path"`
	Tools     map[string]string `json:"tools"`
	Env       map[string]string `json:"env,omitempty"`
	Note      string            `json:"note,omitempty"`
	Source    string            `json:"source,omitempty"`
	UpdatedAt string            `json:"updatedAt,omitempty"`
}

// Bindings 绑定表全量（§4.1）。enabled 默认 false（灰度）；
// autoDetect 默认 prompt —— 上游约定只提示不生效（§1 裁决 6 / §2-L14）。
type Bindings struct {
	Version    int       `json:"version"`
	Enabled    bool      `json:"enabled"`
	AutoDetect string    `json:"autoDetect"`
	MaxDepth   int       `json:"maxDepth"`
	Bindings   []Binding `json:"bindings"`

	path string // 磁盘位置，Save 用；不参与 JSON
}

// autoDetect 三态取值（§4.1）。
const (
	AutoDetectOff    = "off"
	AutoDetectPrompt = "prompt"
	AutoDetectOn     = "on"
)

// DefaultBindings 给出未落盘时的初值：总开关关闭、上游约定只提示、深度 24。
func DefaultBindings() *Bindings {
	return &Bindings{
		Version:    BindingsFormatVersion,
		Enabled:    false,
		AutoDetect: AutoDetectPrompt,
		MaxDepth:   DefaultBindMaxDepth,
		Bindings:   nil,
		path:       BindingsPath(),
	}
}

// BindingsPath 返回 %LOCALAPPDATA%\EnvSwitch\bindings.json（ENVS_HOME 优先）。
func BindingsPath() string { return filepath.Join(settings.Home(), BindingsFileName) }

// GenTablePath 返回 %LOCALAPPDATA%\EnvSwitch\bindings.gen.txt。
func GenTablePath() string { return filepath.Join(settings.Home(), GenTableName) }

func bindingsLockPath() string { return filepath.Join(settings.Home(), BindingsLockName) }

// BindingsDisabled 是进程级逃生门（§10 / §11：ENVS_DISABLE=1）。
// VSCode 片段与任何上层脚本都可以用它一次性关掉解析。
func BindingsDisabled() bool {
	v := strings.TrimSpace(os.Getenv("ENVS_DISABLE"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "on")
}

// Path 返回本次读写用的绑定表路径（便于报告与 --explain）。
func (b *Bindings) Path() string {
	if b == nil || b.path == "" {
		return BindingsPath()
	}
	return b.path
}

// normalized 补齐缺省值，避免磁盘上的老文件缺字段导致行为漂移。
func (b *Bindings) normalized() *Bindings {
	if b == nil {
		return DefaultBindings()
	}
	if b.Version == 0 {
		b.Version = BindingsFormatVersion
	}
	switch strings.ToLower(strings.TrimSpace(b.AutoDetect)) {
	case AutoDetectOff, AutoDetectOn:
		b.AutoDetect = strings.ToLower(strings.TrimSpace(b.AutoDetect))
	default:
		b.AutoDetect = AutoDetectPrompt // §4.1 默认 prompt；未知值一律回落
	}
	if b.MaxDepth <= 0 {
		b.MaxDepth = DefaultBindMaxDepth
	}
	if b.path == "" {
		b.path = BindingsPath()
	}
	return b
}

// Count 返回条目数（GUI 与 doctor 概览用）。
func (b *Bindings) Count() int {
	if b == nil {
		return 0
	}
	return len(b.Bindings)
}

// —— §4.5 读 / 写 / 锁 ——————————————————————————————

var (
	// ErrBindingsReadOnly 锁等待超时后的降级信号：本次只读，不阻塞任何命令（§4.5 / §10）。
	ErrBindingsReadOnly = errors.New("bindings: 写入锁等待超时，本次只读")

	// 锁参数：50 ms 轮询、2 s 超时（§4.5 原文）。测试里临时改小以免拖慢用例。
	bindLockTimeout = 2 * time.Second
	bindLockPoll    = 50 * time.Millisecond
	// bindLockStale：持锁进程崩溃不该永久挡住写入，超过这个年龄的锁文件按陈旧处理。
	bindLockStale = 30 * time.Second
)

// LoadBindings 无锁全量读（§4.5）。
// 返回第二个值是给人看的 warn：文件缺失不算错误（回落默认值即「功能未开启」），
// 解析失败则把坏文件改名 bindings.bad-<ts> 隔离，本次全走全局且不弹错误框。
func LoadBindings() (*Bindings, []string) {
	return loadBindingsAt(BindingsPath())
}

func loadBindingsAt(path string) (*Bindings, []string) {
	def := DefaultBindings()
	def.path = path
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return def, nil
		}
		// 权限/占用等读侧失败：继续用全局（§10「读侧失败仍继续用全局」）
		return def, []string{fmt.Sprintf("绑定配置无法读取，已暂时停用目录绑定并用全局版本：%v", err)}
	}
	var b Bindings
	if err := json.Unmarshal(data, &b); err != nil {
		quar, qerr := quarantineBindings(path)
		if qerr != nil {
			return def, []string{fmt.Sprintf("绑定配置损坏且无法隔离（%v）：%v", qerr, err)}
		}
		return def, []string{fmt.Sprintf("绑定配置无法读取，已暂时停用目录绑定并用全局版本（已隔离为 %s）", filepath.Base(quar))}
	}
	return b.normalized(), nil
}

// quarantineBindings 把损坏文件改名 bindings.bad-<时间戳>，返回新路径（§4.5）。
func quarantineBindings(path string) (string, error) {
	bad := filepath.Join(filepath.Dir(path),
		fmt.Sprintf("bindings.bad-%s%s", time.Now().Format("20060102-150405"), filepath.Ext(path)))
	if _, err := os.Stat(bad); err == nil { // 同一秒内二次损坏：加序号避免改名失败
		for i := 1; i < 100; i++ {
			cand := fmt.Sprintf("%s-%d", bad, i)
			if _, e := os.Stat(cand); e != nil {
				bad = cand
				break
			}
		}
	}
	if err := os.Rename(path, bad); err != nil {
		return "", err
	}
	return bad, nil
}

// SaveBindings 在锁内原子写 bindings.json 并同步重生成 bindings.gen.txt（§4.5 + 接口点 9）。
// 第二个返回值是扁平表的拒写警告（契约 v2 的闸门命中），调用方打到 stderr 即可，
// 不影响写入成功 —— 主表已经是真相，扁平面只是缓存。
func SaveBindings(b *Bindings, ads []Adapter, s *settings.Settings) ([]string, error) {
	if b == nil {
		return nil, W(ErrUsage, "绑定表为空，拒绝写入")
	}
	b.normalized()
	var warns []string
	err := withBindingsLock(func() error {
		data, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFileAtomic(b.Path(), append(data, '\n')); err != nil {
			return err
		}
		w, err := writeGenTableLocked(b, s, ads)
		warns = append(warns, w...)
		return err
	})
	return warns, err
}

// withBindingsLock 用 O_CREATE|O_EXCL 的 bindings.lock 做跨进程互斥。
// 超时不报错而是返回 ErrBindingsReadOnly，调用方按「只读继续」处理（§4.5）。
func withBindingsLock(fn func() error) error {
	lockPath := bindingsLockPath()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	deadline := time.Now().Add(bindLockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(fmt.Sprintf("%d %s\n", os.Getpid(), time.Now().Format(time.RFC3339)))
			_ = f.Close()
			runErr := fn()
			if rmErr := os.Remove(lockPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) && runErr == nil {
				runErr = rmErr
			}
			return runErr
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		// 陈旧锁接管：持锁进程崩掉后不该永久挡住写入
		if fi, e := os.Stat(lockPath); e == nil && time.Since(fi.ModTime()) > bindLockStale {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return W(ErrBindingsReadOnly, "等待 bindings.lock 超过 %s", bindLockTimeout)
		}
		time.Sleep(bindLockPoll)
	}
}

// writeFileAtomic 沿用 settings.Save 的手法：同目录 .tmp + Rename。
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CheckBindingsWritable 原子写自检（§7.3 第 7 项）：临时文件 + rename，验完即删，
// 不触碰 bindings.json 本体。
func CheckBindingsWritable() error {
	dir := filepath.Dir(BindingsPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "bindings.probe.tmp")
	out := filepath.Join(dir, "bindings.probe.out")
	_ = os.Remove(out)
	if err := os.WriteFile(tmp, []byte(genTableHeader+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Remove(out)
}

// —— §4.2 路径归一（纯函数，顺序敏感）————————————————

// Windows 扩展前缀（§4.2 要求剥掉的三种形态）。
var extPathPrefixes = []string{`\\?\`, `\??\`, `\\.\`, `\.\`}

// winHooks 由 bindings_windows.go 在 init 里替换；默认恒等实现保证本文件可独立测试。
var (
	winLongPath   = func(p string) string { return p }      // GetLongPathNameW 展 8.3
	winSubstRoot  = func(drive string) string { return "" } // QueryDosDeviceW 解 subst 盘
	winFinalPath  = func(p string) string { return "" }     // GetFinalPathNameByHandle 目标路径
	winAutoRunGet = func() (string, bool, error) { return "", false, nil }
	winAutoRunSet = func(v string, remove bool) error { return nil }
)

// NormalizePath 按 §4.2 的顺序归一：去成对引号 → 剥扩展前缀 → Abs+Clean →
// 正斜杠转反斜杠 → GetLongPathNameW 展 8.3（失败保留原值，不致命）→ QueryDosDevice 解 subst 盘。
// 空输入返回空串。
func NormalizePath(p string) string {
	s := strings.TrimSpace(p)
	s = stripPairQuotes(s)
	s = stripExtPrefix(s)
	if s == "" {
		return ""
	}
	abs, err := filepath.Abs(s)
	if err != nil {
		abs = filepath.Clean(s)
	}
	n := filepath.Clean(abs)
	n = strings.ReplaceAll(n, "/", `\`)
	// 展 8.3 短名：失败（目录不存在、权限）保留原值，归一本身不致命
	if lp := winLongPath(n); lp != "" {
		n = strings.ReplaceAll(filepath.Clean(lp), "/", `\`)
	}
	// subst / dos 设备盘：X: 实际指向 D:\somewhere，键要落在真实位置上
	if len(n) >= 2 && n[1] == ':' {
		if real := winSubstRoot(n[:2]); real != "" && !strings.EqualFold(filepath.Clean(real), n[:2]) {
			merged := strings.TrimSuffix(real, `\`) + n[2:]
			if c := filepath.Clean(merged); c != "" {
				n = strings.ReplaceAll(c, "/", `\`)
			}
		}
	}
	return n
}

// CmpKey 是匹配唯一使用的键：归一结果再 ToLower（§4.2）。
func CmpKey(p string) string { return strings.ToLower(NormalizePath(p)) }

// BindingKeys 返回一条路径应登记的全部 cmpKey：链接路径本身 +
// GetFinalPathNameByHandle 解析后的目标路径（治 §2-L11 的六种形态不一致）。
// UNC 与裸盘根照样返回，由调用侧的匹配规则拒配。
func BindingKeys(p string) []string {
	n := NormalizePath(p)
	if n == "" {
		return nil
	}
	out := []string{strings.ToLower(n)}
	if tgt := winFinalPath(n); tgt != "" {
		t := strings.ToLower(NormalizePath(tgt))
		if t != "" && t != out[0] {
			out = append(out, t)
		}
	}
	return out
}

func stripPairQuotes(s string) string {
	for len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = strings.TrimSpace(s[1 : len(s)-1])
			continue
		}
		break
	}
	return s
}

func stripExtPrefix(s string) string {
	for _, pre := range extPathPrefixes {
		if len(s) >= len(pre) && strings.EqualFold(s[:len(pre)], pre) {
			return s[len(pre):]
		}
	}
	return s
}

// IsUNCKey 判定 UNC 键（\\server\share）。UNC 属独立命名空间，不参与前缀匹配（§4.2）。
func IsUNCKey(cmpKey string) bool { return strings.HasPrefix(cmpKey, `\\`) }

// IsBareRoot 判定裸盘根（e: / e:\）—— 绑到盘根等于绑整块盘，拒配（§4.2）。
func IsBareRoot(cmpKey string) bool {
	k := strings.TrimSuffix(cmpKey, `\`)
	return len(k) == 2 && k[1] == ':'
}

// keyMatchable 该键能否参与目录前缀匹配。
func keyMatchable(k string) bool {
	return k != "" && !IsUNCKey(k) && !IsBareRoot(k)
}

// hasBoundaryPrefix child 是否位于 parent 之下（含相等），按分隔符边界，
// 治 §2-L12：p\ab 不得被 p\a 命中、E:\proj 不得命中 E:\proj-front。
func hasBoundaryPrefix(parent, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+`\`)
}

// —— §4.3 匹配 ——————————————————————————————————

// bindingMatchableKeys 取一条绑定可参与匹配的全部键（已剔除 UNC / 裸盘根）。
func bindingMatchableKeys(bd Binding) []string {
	var out []string
	for _, k := range BindingKeys(bd.Path) {
		if keyMatchable(k) {
			out = append(out, k)
		}
	}
	return out
}

// MatchBinding 最长前缀命中（§4.3-1）；命中不了返回 nil。
func MatchBinding(cmpCwd string, bs []Binding) *Binding {
	chain := MatchChain(cmpCwd, bs)
	if len(chain) == 0 {
		return nil
	}
	return &chain[len(chain)-1]
}

// BindingHit 一次命中的记录（供 bind why 展开链）。
type BindingHit struct {
	Path string `json:"path"`
	Key  string `json:"key"`
}

// MatchChain 由浅到深返回所有命中的条目（§4.3-2 逐工具独立回退的基础）。
// 同长度（同一目录被登记两次）时后者胜，保证「最后一次写表」的语义可预期。
func MatchChain(cmpCwd string, bs []Binding) []Binding {
	cwd := strings.ToLower(NormalizePath(cmpCwd))
	if cwd == "" {
		return nil
	}
	type rec struct {
		bd  Binding
		key string
	}
	var hits []rec
	for i := range bs {
		for _, k := range bindingMatchableKeys(bs[i]) {
			if hasBoundaryPrefix(k, cwd) {
				hits = append(hits, rec{bs[i], k})
				break
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return len(hits[i].key) < len(hits[j].key) })
	out := make([]Binding, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.bd)
	}
	return out
}

// —— §5 执行层白名单（首期硬编码，扩展须评审）——————————————

// bindTier1 档①：一等公民，版本目录整体前置即可（§5）。
// npx / pnpx 是 npm / pnpm 的入口命令名，绑定归一到宿主工具 ID 后解析（见 bindHostTool）。
var bindTier1 = map[string]bool{
	"node": true, "npm": true, "npx": true, "pnpm": true, "pnpx": true,
	"yarn": true, "python": true, "go": true,
}

// bindTier2 档②：机制上支持但必须附带环境变量注入，属 §11 P3，本次未开放。
var bindTier2 = map[string]string{
	"java":   "java 需要 JAVA_HOME 注入才完整生效（§5 档②，属 §11 P3），本次未开放绑定",
	"maven":  "maven 需要 JAVA_HOME + MAVEN_HOME 注入且缺 shim 入口（§5 档②），本次未开放绑定",
	"gradle": "gradle 需要 JAVA_HOME + GRADLE_HOME 注入且缺 shim 入口（§5 档②），本次未开放绑定",
}

// bindTier5 档⑤：绑了会误导，绑定命令直接拒绝（§5 / §12）。
var bindTier5 = map[string]string{
	"pip":    "pip 随解释器分发，版本不归 EnvSwitch 所有（§5 档⑤），绑定会误导",
	"nvm":    "nvm-windows 本身就是另一套 junction 方案，与 EnvSwitch 语义互斥（§5 档⑤ / §12）",
	"docker": "docker 版本在 daemon 里，换 CLI 目录无意义（§5 档⑤）",
}

// bindHostTool 命令名 → 承载它的适配器 ID：npx 随 npm、pnpm 的 pnpx 随 pnpm。
var bindHostTool = map[string]string{"npx": "npm", "pnpx": "pnpm"}

// nativeHint 档③让位时给「怎么反过来让绑定生效」的修法（bind why 用，§5）。
var nativeHint = map[string]string{
	"dotnet": "要让 EnvSwitch 决定 dotnet 版本，请先移除或改名 global.json。",
	"go":     "要让绑定压过 go.mod 的 toolchain，请在绑定里带 env GOTOOLCHAIN=local：envs bind go <ver> --env GOTOOLCHAIN=local",
	"pnpm":   "packageManager 由 corepack 自管；要固定包管理器版本请改 package.json 的 packageManager 字段，或删掉它再用绑定。",
	"yarn":   "packageManager 由 corepack 自管；要固定包管理器版本请改 package.json 的 packageManager 字段，或删掉它再用绑定。",
	"python": "虚拟环境/uv 的优先级高于目录绑定（正确行为，§10 venv 行）；要由绑定决定 python，请 deactivate 或删除 .python-version。",
	"gradle": "gradlew 自带发行版；要换 gradle 请用 wrapper 属性，或删掉 wrapper 再用绑定。",
	"maven":  "mvnw 自带发行版；要换 maven 请用 wrapper 属性，或删掉 wrapper 再用绑定。",
}

// BindGate 判定一个工具 ID 能否被绑定。返回 (允许, 档位, 说明)。
// 档④（其余 16 工具）落 default 分支：机制上能前置但首期不开放，扩展须评审。
func BindGate(tool string) (bool, string, string) {
	id := strings.ToLower(strings.TrimSpace(tool))
	if reason, bad := bindTier5[id]; bad {
		return false, "5", reason
	}
	if reason, pending := bindTier2[id]; pending {
		return false, "2", reason + "（等待前置条件：§11 P3 的 JAVA_HOME 注入落地）"
	}
	if bindTier1[id] {
		return true, "1", "档① 一等公民：版本目录整体前置即可，全链路一致（§5）"
	}
	switch id {
	case "rust", "php", "deno", "bun", "uv", "git", "kubectl", "terraform", "redis", "dotnet":
		return false, "4", fmt.Sprintf("%s 属 §5 档档③/④：或已有上游原生按目录选型，或没有按目录换版本的真实诉求，首期不开放", id)
	}
	return false, "-", fmt.Sprintf("未知工具 %q；执行层白名单首期硬编码 node/npm/npx/pnpm/pnpx/yarn/python/go，扩展须评审（§5 档④）", id)
}

// BindAliasNote 说明命令名与宿主工具的关系（供 bind / bind why 提示）。
func BindAliasNote(tool string) string {
	if host, ok := bindHostTool[strings.ToLower(strings.TrimSpace(tool))]; ok {
		return fmt.Sprintf("%s 由 %s 的目录自带入口提供（§2-L4）", tool, host)
	}
	return ""
}

// BindHostOf 返回命令名承载它的宿主工具 ID（npx→npm、pnpx→pnpm；其余原样）。
func BindHostOf(tool string) string {
	id := strings.ToLower(strings.TrimSpace(tool))
	if h, ok := bindHostTool[id]; ok {
		return h
	}
	return id
}

// —— §4.3 表达式解析 ————————————————————————————

// ConstraintExprError 是「这是约束，不能定位具体版本」的固定措辞（§4.3 末段）。
const ConstraintExprError = "这是约束，不能定位具体版本"

// LooksLikeConstraint 判断版本表达式是不是 semver 范围 / lts 之类不可定位的写法。
// 首期只接受 16、16.20.2、latest 三种形态（§4.3）。
func LooksLikeConstraint(expr string) (bool, string) {
	v := strings.TrimSpace(expr)
	if v == "" {
		return false, "版本表达式为空"
	}
	if v == "latest" {
		return false, ""
	}
	for _, bad := range []string{"^", "~", ">", "<", "=", "*", "|", " ", "lts", "LTS", "current", "x", "X"} {
		if strings.Contains(v, bad) {
			return true, fmt.Sprintf("%s：%q 是范围/别名写法，首期只接受 16、16.20.2、latest（§4.3，不做 semver 范围）", ConstraintExprError, v)
		}
	}
	// 允许 16 / 16.20 / 16.20.2 与可选 v 前缀，其余（如 16.20.2-nightly、build+meta）不可定位
	head := strings.TrimPrefix(v, "v")
	for _, seg := range strings.Split(head, ".") {
		if seg == "" {
			return true, fmt.Sprintf("%s：%q 写法不完整", ConstraintExprError, v)
		}
		for _, r := range seg {
			if r < '0' || r > '9' {
				return true, fmt.Sprintf("%s：%q 含非数字段，首期只接受 16、16.20.2、latest（§4.3）", ConstraintExprError, v)
			}
		}
	}
	return false, ""
}

// BindingInjection 一个工具解析成功后的注入项。
type BindingInjection struct {
	Tool       string `json:"tool"`
	Expr       string `json:"expr"`
	Version    string `json:"version"`
	BinDir     string `json:"binDir"`
	Source     string `json:"source"`  // store / nvm / detected
	BoundAt    string `json:"boundAt"` // 声明它的绑定目录（可能是父目录）
	PathUSable bool   `json:"pathUsable"`
}

// BindingSkip 一个被跳过 / 未生效的声明，带原因（bind why 的正文）。
type BindingSkip struct {
	Tool   string `json:"tool,omitempty"`
	Expr   string `json:"expr,omitempty"`
	Kind   string `json:"kind"` // disabled / denied / native / constraint / not-installed / not-usable / poisoned / missing-dir
	Reason string `json:"reason"`
}

// BindingResolve 一次目录解析的完整结果（§4.3 / §6.3 / §7.1 共用）。
type BindingResolve struct {
	Cwd     string             `json:"cwd"`
	CmpKey  string             `json:"cmpKey"`
	Enabled bool               `json:"enabled"`
	Hits    []BindingHit       `json:"hits,omitempty"`   // 由浅到深
	Inject  []BindingInjection `json:"inject,omitempty"` // 实际要前置的目录
	Env     map[string]string  `json:"env,omitempty"`    // §4.4 合并后的覆盖
	Skips   []BindingSkip      `json:"skips,omitempty"`
	Warns   []string           `json:"warns,omitempty"`
	Notes   []string           `json:"notes,omitempty"` // 上游约定提示（§1 裁决 6：只提示不生效）
}

// ResolveBindings 解析 cwd（空 = 进程当前目录）。任何失败都只增加 Skips/Warns，
// 从不返回错误 —— 这是 §10「任何异常绝不阻塞 npm run dev」的落点。
func ResolveBindings(cwd string, b *Bindings, s *settings.Settings, ads []Adapter) *BindingResolve {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	r := &BindingResolve{Cwd: cwd, CmpKey: CmpKey(cwd), Env: map[string]string{}}
	if BindingsDisabled() {
		r.Notes = append(r.Notes, "ENVS_DISABLE=1：本进程跳过目录绑定解析，用全局版本（§11 逃生门）。")
		return r
	}
	if b == nil {
		var warns []string
		b, warns = LoadBindings()
		r.Warns = append(r.Warns, warns...)
	}
	b = b.normalized()
	r.Enabled = b.Enabled
	if !b.Enabled {
		r.Skips = append(r.Skips, BindingSkip{Kind: "disabled",
			Reason: "目录绑定总开关未开启（§4.1 enabled 默认 false）；用 envs bind enable 开启。"})
		return r
	}

	chain := MatchChain(r.CmpKey, b.Bindings)
	for _, bd := range chain {
		r.Hits = append(r.Hits, BindingHit{Path: bd.Path, Key: CmpKey(bd.Path)})
	}
	if len(chain) == 0 && b.AutoDetect != AutoDetectOn {
		return r
	}

	markers := scanProjectMarkers(r.Cwd, b.MaxDepth)

	// §4.4 env 先合并：由浅到深，深者胜。它只在会话 / envs run 内注入，绝不写注册表；
	// 同时供下面的「go 例外」判断使用（用户显式 GOTOOLCHAIN=local 时绑定优先于 go.mod）。
	chainEnv := map[string]string{}
	for _, bd := range chain {
		for k, v := range bd.Env {
			k = strings.TrimSpace(k)
			if !safeEnvPair(k, v) {
				r.Warns = append(r.Warns, fmt.Sprintf("绑定 env 键或值非法（键需为标识符、值不得含换行/制表），已忽略：%q", k))
				continue
			}
			chainEnv[k] = v
		}
	}

	// 逐工具独立回退：由深到浅，第一个声明该工具的绑定胜出（§4.3-2）
	declared := map[string]struct{ expr, from string }{}
	order := []string{}
	for i := len(chain) - 1; i >= 0; i-- {
		for tool, expr := range chain[i].Tools {
			id := strings.ToLower(strings.TrimSpace(tool))
			if _, seen := declared[id]; !seen {
				declared[id] = struct{ expr, from string }{strings.TrimSpace(expr), chain[i].Path}
				order = append(order, id)
			}
		}
	}
	sort.Strings(order) // 输出稳定，便于 --json 断言

	for _, id := range order {
		decl := declared[id]
		// 档③原生让位：项目里存在上游选型标记 → 不注入对应工具（§5）。
		// 唯一例外：go 绑定显式带 GOTOOLCHAIN=local 时以绑定为准 ——
		// §5 明确「绑定 go 时必须注入 GOTOOLCHAIN=local，否则用户绑了 1.22、go 自己偷偷下 1.26」。
		if m, native := markers.native[id]; native {
			if id == "go" && strings.EqualFold(chainEnv["GOTOOLCHAIN"], "local") {
				r.Notes = append(r.Notes, fmt.Sprintf(
					"检测到 %s（%s），但绑定已显式给 GOTOOLCHAIN=local，按绑定注入 go（§5 档③）", m.File, m.Detail))
			} else {
				r.Skips = append(r.Skips, BindingSkip{Tool: id, Expr: decl.expr, Kind: "native",
					Reason: fmt.Sprintf("检测到 %s（%s），%s 交还给上游原生机制，EnvSwitch 不注入（§5 档③）。%s",
						m.File, m.Detail, id, nativeHint[id])})
				continue
			}
		}
		if ok, tier, why := BindGate(id); !ok {
			r.Skips = append(r.Skips, BindingSkip{Tool: id, Expr: decl.expr, Kind: skipKindOfTier(tier), Reason: why})
			continue
		}
		if bad, why := LooksLikeConstraint(decl.expr); bad {
			r.Skips = append(r.Skips, BindingSkip{Tool: id, Expr: decl.expr, Kind: "constraint", Reason: why})
			continue
		}
		host := id
		if h, ok := bindHostTool[id]; ok {
			host = h
		}
		ad := adapterOf(ads, host)
		if ad == nil {
			r.Skips = append(r.Skips, BindingSkip{Tool: id, Expr: decl.expr, Kind: "denied",
				Reason: fmt.Sprintf("本机没有 %s 的适配器，无法定位版本", host)})
			continue
		}
		ins, why := resolveInstall(ad, decl.expr, s)
		if ins == nil {
			kind := "not-installed"
			if why != "" && strings.Contains(why, "目录") {
				kind = "missing-dir"
			}
			skip := BindingSkip{Tool: id, Expr: decl.expr, Kind: kind,
				Reason: fmt.Sprintf("%s 绑定的 %s %s 未安装，本次用全局版本。%s", decl.from, host, decl.expr, why)}
			r.Skips = append(r.Skips, skip)
			// §10 第一行：落全局 + warn，且给出可复制的补装命令
			gv := "全局版本"
			if cur, _, err := CurrentInstall(ad, s); err == nil && cur != nil {
				gv = "全局 " + cur.Version
			} else if det := DetectedOnPATH(PathExeName(ad)); det != nil {
				gv = "全局（PATH 上的 " + det.BinDir + "）"
			}
			r.Warns = append(r.Warns, fmt.Sprintf("提示：%s 绑定的 %s %s 未安装，本次用%s。补装：envs install %s %s",
				decl.from, host, decl.expr, gv, host, decl.expr))
			continue
		}
		// 安全断言（§4.3-4）：结果目录必须落在 store ∪ NvmRoot ∪ detected 白名单内
		if !AllowBindingDir(ins.BinDir, s, ad) {
			r.Skips = append(r.Skips, BindingSkip{Tool: id, Expr: decl.expr, Kind: "poisoned",
				Reason: fmt.Sprintf("拒绝注入 %s：目录不在版本库 ∪ nvm ∪ PATH 检测白名单内（§4.3-4 防绑定表投毒）", ins.BinDir)})
			r.Warns = append(r.Warns, fmt.Sprintf("绑定表指向的目录 %s 不被信任，已忽略并回退全局版本。", ins.BinDir))
			continue
		}
		usable := PathUsableTool(ad)
		r.Inject = append(r.Inject, BindingInjection{
			Tool: id, Expr: decl.expr, Version: ins.Version, BinDir: ins.BinDir,
			Source: ins.Source, BoundAt: decl.from, PathUSable: usable,
		})
		if !usable {
			r.Skips = append(r.Skips, BindingSkip{Tool: id, Expr: decl.expr, Kind: "not-usable",
				Reason: fmt.Sprintf("%s 的版本根里没有 PATH 可直接调用的同名入口，前置无效；它随 node 目录自带（§2-L4），绑定 node 即可切换", id)})
		}
	}

	// §4.4 合并结果落到解析块；只在会话 / envs run 内注入，绝不写注册表
	r.Env = chainEnv
	// autoDetect=on：采纳 .nvmrc / .node-version 的 pin 值（lead R6 裁决的明确语义）。
	// 只认 pin 文件里的值且必须能被 MatchVersion 命中已装版本；engines 约束永不进这里。
	// 显式绑定优先于上游约定：链上已经声明过 node 就完全不启用。
	if b.AutoDetect == AutoDetectOn {
		if _, declaredNode := declared["node"]; !declaredNode && !hasTool(r.Inject, "node") {
			switch {
			case markers.pin == "":
				// 没有 pin 文件：无事可做（engines 的提示已经在 Notes 里）
			case hasTool(r.Inject, "node"):
				// 上面已注入
			default:
				ad := adapterOf(ads, "node")
				if ad == nil {
					r.Skips = append(r.Skips, BindingSkip{Tool: "node", Expr: markers.pin, Kind: "denied",
						Reason: "autoDetect=on 需要 node 适配器，本机没有"})
				} else if bad, why := LooksLikeConstraint(markers.pin); bad {
					r.Skips = append(r.Skips, BindingSkip{Tool: "node", Expr: markers.pin, Kind: "constraint",
						Reason: fmt.Sprintf("%s（来自 %s）：autoDetect=on 也只采纳 pin 值", why, markers.pinFile)})
				} else if ins, why := resolveInstall(ad, markers.pin, s); ins == nil {
					r.Skips = append(r.Skips, BindingSkip{Tool: "node", Expr: markers.pin, Kind: "not-installed",
						Reason: fmt.Sprintf("autoDetect=on 采纳 %s 的 %q，但版本不可用：%s", filepath.Base(markers.pinFile), markers.pin, why)})
				} else if !AllowBindingDir(ins.BinDir, s, ad) {
					r.Skips = append(r.Skips, BindingSkip{Tool: "node", Expr: markers.pin, Kind: "poisoned",
						Reason: fmt.Sprintf("autoDetect=on 采纳的目录 %s 不在白名单内（§4.3-4）", ins.BinDir)})
				} else {
					r.Inject = append(r.Inject, BindingInjection{
						Tool: "node", Expr: markers.pin, Version: ins.Version, BinDir: ins.BinDir,
						Source: ins.Source, BoundAt: markers.pinFile, PathUSable: PathUsableTool(ad),
					})
					r.Notes = append(r.Notes, fmt.Sprintf(
						"autoDetect=on：已采纳 %s 的 pin %q → node %s（显式绑定 envs bind node <ver> 会覆盖它）",
						markers.pinFile, markers.pin, ins.Version))
				}
			}
		}
	}
	// §10「webpack4 + node≥18 的 md4」：只读 devDependencies.webpack 的前缀，不做版本定位
	if w := webpackMd4Warning(r, markers); w != "" {
		r.Warns = append(r.Warns, w)
	}
	// go 绑定要压住 go 自己的工具链下载，否则绑定形同虚设（§5 档③）
	if hasTool(r.Inject, "go") {
		if _, set := r.Env["GOTOOLCHAIN"]; !set {
			r.Env["GOTOOLCHAIN"] = "local"
			r.Notes = append(r.Notes, "已注入 GOTOOLCHAIN=local：否则 go 会按 go.mod 自下工具链，绑定形同虚设（§5 档③）")
		}
	}

	// 上游约定提示：默认只提示不生效（§1 裁决 6 / §2-L14）
	if b.AutoDetect != AutoDetectOff {
		r.Notes = append(r.Notes, markers.prompts...)
	}
	return r
}

func skipKindOfTier(tier string) string {
	switch tier {
	case "5":
		return "denied"
	case "2", "4":
		return "whitelist"
	default:
		return "whitelist"
	}
}

func hasTool(inj []BindingInjection, id string) bool {
	for _, x := range inj {
		if x.Tool == id {
			return true
		}
	}
	return false
}

func adapterOf(ads []Adapter, id string) Adapter {
	for _, ad := range ads {
		if ad.ID() == id {
			return ad
		}
	}
	return nil
}

// PathUsableTool 判断该工具的 Install.BinDir 直接前置进 PATH 是否有效。
// JSRunner（npm/pnpm/yarn）的版本根里只有 bin/*-cli.js，PATH 上叫不出同名命令 ——
// 真正生效的是 node 目录自带的入口（§2-L4）与 bin\shims（层 2，归 agent-A）。
func PathUsableTool(ad Adapter) bool {
	if ad == nil {
		return false
	}
	if _, isJS := ad.(JSRunner); isJS {
		return false
	}
	return true
}

// resolveInstall 表达式 → 已装版本（§4.3-3）：候选 = ListInstalls（含 nvm 收编），
// 匹配走现成 MatchVersion（latest / 精确 / 前缀）。绝不自动下载（§10 第一行）。
func resolveInstall(ad Adapter, expr string, s *settings.Settings) (*Install, string) {
	vers := ListInstallsFast(ad, s)
	if len(vers) == 0 {
		return nil, fmt.Sprintf("本机没有任何 %s 版本（store 与收编来源均为空）", ad.ID())
	}
	names := make([]string, 0, len(vers))
	for _, v := range vers {
		names = append(names, v.Version)
	}
	got, err := MatchVersion(names, expr)
	if err != nil {
		return nil, fmt.Sprintf("已装 %s；%v", strings.Join(names, " / "), err)
	}
	for i := range vers {
		if vers[i].Version != got {
			continue
		}
		if fi, e := os.Stat(vers[i].BinDir); e != nil || !fi.IsDir() {
			return nil, fmt.Sprintf("已装版本目录 %s 不存在（外接盘未挂或已改名）", vers[i].BinDir)
		}
		return &vers[i], ""
	}
	return nil, ""
}

// AllowBindingDir 安全断言（§4.3-4）：允许注入的目录白名单 =
// 版本库 store ∪ NvmRoot ∪ 注册表 PATH 上检测到的该工具目录。
// 投毒的 bindings.json 只能写版本号，写不进任意目录。
func AllowBindingDir(dir string, s *settings.Settings, ad Adapter) bool {
	d := NormalizePath(dir)
	if d == "" {
		return false
	}
	roots := []string{settings.StoreDir(s)}
	if r := NvmRoot(); r != "" {
		roots = append(roots, r)
	}
	if ad != nil {
		if exe := PathExeName(ad); exe != "" {
			if det := DetectedOnPATH(exe); det != nil && det.BinDir != "" {
				roots = append(roots, det.BinDir)
			}
		}
	}
	for _, rt := range roots {
		if r := NormalizePath(rt); r != "" && isUnderPath(d, r) {
			return true
		}
	}
	return false
}

// safeEnvPair 绑定 env 的可注入性校验（§2-L9 / §10「& 注入面」）。
// 契约 v2 之后 gen 表第 4 列整体 percent-encode，元字符不再威胁表格结构；
// 这里守的是「键必须是合法环境变量名 + 值不含换行/制表/NUL」，
// 以及 PowerShell / bash / cmd 三侧的注入底线（值里的 & ^ " % 由各自引号与闸门处理）。
func safeEnvPair(k, v string) bool {
	return validEnvKey(k) && !strings.ContainsAny(v, "\r\n\t\x00")
}

// validEnvKey 环境变量名：字母或下划线开头，其后字母数字下划线。
func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		ok := r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// EnvPairsString 把 env 映射成 gen 表第 4 列。契约 v2（lead 2026-11-19 定死）：
//
//	空串，或 k=v&k2=v2；key 与 value **分别**做 percent-encoding，
//	不编码集仅 [A-Za-z0-9._:-]，空格编成 %20（不用 +），% 自身编成 %25。
//
// 因此编码域内不可能出现裸的 = & " ^ ; 反引号 与换行，bat 读侧可放心切分。
// 第 4 列之外的前三列保持明文（读侧的绝对路径结构闸门依赖明文）。
func EnvPairsString(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		if !validEnvKey(k) || strings.ContainsAny(env[k], "\r\n\t") {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString(genEnvPairSep)
		}
		sb.WriteString(percentEncode(k))
		sb.WriteString("=")
		sb.WriteString(percentEncode(env[k]))
	}
	return sb.String()
}

// percentEncode 实现契约 v2 的编码集：字母数字与 . _ : - 原样，其余逐字节转 %XX（大写）。
// 不用 url.QueryEscape：它的免编码集是 [A-Za-z0-9-_.~] 且把 ':' 编成 %3A，与本合同不一致。
func percentEncode(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '.', c == '_', c == ':', c == '-':
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, "%%%02X", c)
		}
	}
	return sb.String()
}

// percentDecode 是反向操作；非法转义序列返回 ok=false（读侧据此丢弃该对，不 panic）。
func percentDecode(s string) (string, bool) {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			if c == '+' {
				return "", false // 契约明确：空格是 %20，不是 +
			}
			sb.WriteByte(c)
			continue
		}
		if i+2 >= len(s) {
			return "", false
		}
		hi, hok := hexVal(s[i+1])
		lo, lok := hexVal(s[i+2])
		if !hok || !lok {
			return "", false
		}
		sb.WriteByte(hi<<4 | lo)
		i += 2
	}
	return sb.String(), true
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// ParseEnvPairs 反向解析 gen 表第 4 列（测试与自检用；读侧真实现归 agent-A 的 bat）。
func ParseEnvPairs(s string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(s) == "" {
		return out
	}
	for _, p := range strings.Split(s, genEnvPairSep) {
		if p == "" {
			continue
		}
		kc, vc, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		k, kok := percentDecode(kc)
		v, vok := percentDecode(vc)
		if !kok || !vok || !validEnvKey(k) {
			continue
		}
		out[k] = v
	}
	return out
}

// —— gen 表与注入（§4.5 与 agent-A 的固定接口）——————————————————

// InjectDirs 返回要前置进 PATH 的目录（已按注入顺序；不可前置 PATH 的工具已剔除）。
func (r *BindingResolve) InjectDirs() []string {
	var out []string
	if r == nil {
		return out
	}
	seen := map[string]bool{}
	for _, x := range r.Inject {
		if !x.PathUSable || x.BinDir == "" {
			continue
		}
		k := strings.ToLower(x.BinDir)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, x.BinDir)
	}
	return out
}

// ApplyEnv 把解析结果作用到一份环境块上（层 3 的 envs run / envs shell 用，§6.3 / §4.4）。
// PATH 用「前置注入目录 + 原值」，绝不叠加；env 覆盖只在进程块内生效，不写注册表。
func (r *BindingResolve) ApplyEnv(base []string) []string {
	if r == nil {
		return base
	}
	pathKey := ""
	pathVal := ""
	for _, kv := range base {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(k, "PATH") {
			pathKey, pathVal = k, v
			break
		}
	}
	if pathKey == "" {
		pathKey, pathVal = "PATH", ""
	}
	dirs := r.InjectDirs()
	// 绝不叠加（§6.1-4 的进程块版对应物）：先把注入目录从原 PATH 中摘掉，再整体前置，
	// 这样对「已注入过一次的环境块」重复作用结果不变（单测 TestBindingsEnvOverlayAndApply 断言）。
	tail := make([]string, 0, 8)
	for _, seg := range strings.Split(pathVal, ";") {
		if seg == "" {
			continue
		}
		dup := false
		for _, d := range dirs {
			if strings.EqualFold(trimPathQuotes(seg), trimPathQuotes(d)) {
				dup = true
				break
			}
		}
		if !dup {
			tail = append(tail, seg)
		}
	}
	parts := make([]string, 0, len(dirs)+len(tail))
	parts = append(parts, dirs...)
	parts = append(parts, tail...)
	merged := strings.Join(parts, ";")

	out := make([]string, 0, len(base)+len(r.Env)+1)
	setKeys := map[string]bool{"PATH": true}
	for _, kv := range base {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(k, "PATH") {
			out = append(out, pathKey+"="+merged)
			continue
		}
		if v, hit := r.Env[k]; hit {
			out = append(out, k+"="+v)
			setKeys[strings.ToUpper(k)] = true
			continue
		}
		out = append(out, kv)
	}
	for _, k := range sortedKeys(r.Env) {
		if setKeys[strings.ToUpper(k)] {
			continue
		}
		out = append(out, k+"="+r.Env[k])
	}
	return out
}

// trimPathQuotes 去掉 PATH 片段两侧的引号并规范化分隔符，用于「同一目录」的比较。
func trimPathQuotes(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	if c := filepath.Clean(s); c != "." {
		s = c
	}
	return strings.TrimRight(s, string(filepath.Separator))
}

// EnvKeys 返回 env 覆盖的有序键（CLI 打印 --print env 用）。
func (r *BindingResolve) EnvKeys() []string { return sortedKeys(r.Env) }

// —— 层 3 的命令查找（§6.3；lead R6）————————————————

// LookupInEnv 在**给定环境块的 PATH** 上按 Windows 语义查找可执行文件。
//
// 为什么必须自带一份：exec.LookPath / exec.Command 用的是 envs.exe **自己进程**的 PATH，
// 在系统 PATH 抢位的机器上（本机就是：C:\nvm4w\nodejs 在前，§2-L1/L2）会先命中全局那个，
// 之后塞进 cmd.Env 的新 PATH 对已经解析出来的绝对路径不起作用 —— 绑定就成了假生效（R6）。
// 正确做法：先构造环境块，再在该环境块的 PATH 上查找，把绝对路径直接喂给 exec.Cmd.Path。
//
// 语义对齐 cmd：逐目录（PATH 顺序）× 逐个 PATHEXT（.COM;.EXE;.BAT;.CMD 顺序），
// 同名时 .exe 压 .cmd（与 §2-L6 同目录实测一致）；带分隔符的入参按原样解析不查 PATH；
// 目录与不存在的一律跳过。找不到返回 ("", false)。
func LookupInEnv(env []string, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	exts := pathExtOf(env)
	// 写了路径（含分隔符或盘符）：就是显式指定，不查 PATH
	if strings.ContainsAny(name, `/\`) || (len(name) >= 2 && name[1] == ':') {
		return firstExecutable(filepath.Dir(name), filepath.Base(name), exts)
	}
	for _, dir := range strings.Split(envValueCI(env, "PATH"), ";") {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if p, ok := firstExecutable(dir, name, exts); ok {
			return p, true
		}
	}
	return "", false
}

// pathExtOf 取环境块里的 PATHEXT；没有则按 Windows 默认序列；非 Windows 只试裸名。
func pathExtOf(env []string) []string {
	if runtime.GOOS != "windows" {
		return []string{""}
	}
	if pe := envValueCI(env, "PATHEXT"); pe != "" {
		var out []string
		for _, e := range strings.Split(pe, ";") {
			if e = strings.TrimSpace(e); e != "" {
				out = append(out, strings.ToLower(e))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{".com", ".exe", ".bat", ".cmd"}
}

func firstExecutable(dir, base string, exts []string) (string, bool) {
	try := func(p string) (string, bool) {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
		return "", false
	}
	if filepath.Ext(base) != "" {
		if p, ok := try(filepath.Join(dir, base)); ok {
			return p, true
		}
	}
	for _, e := range exts {
		if p, ok := try(filepath.Join(dir, base+e)); ok {
			return p, true
		}
	}
	return "", false
}

// envValueCI 不区分大小写地取环境块里的变量（Windows 的 PATH/Path 混排是常态）。
func envValueCI(env []string, key string) string {
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RenderGenTable 生成扁平表文本（§4.5 与 agent-A 的固定接口，格式不得改动）。
// 契约 v2：UTF-8 无 BOM、CRLF、首行 `#version=1`、数据行**恰好 4 列**制表符分隔
// `cmpKey<TAB>tool<TAB>binDir<TAB>envPairs`；第 4 列 percent-encode（见 EnvPairsString），
// 前 3 列保持明文；binDir 含 `%` 或换行 → 该行拒写并回传 warn（bat 的 % 展开语义穿不过去）。
// enabled=false 时只输出首行 —— 钩子读到空表即「什么都不做」，这就是灰度总开关的落点。
func RenderGenTable(b *Bindings, s *settings.Settings, ads []Adapter) (string, []string) {
	if b == nil {
		b = DefaultBindings()
	}
	b = b.normalized()
	var rows []string
	var warns []string
	type row struct{ key, tool, dir, env string }
	var picked []row
	if b.Enabled {
		// 每个登记键各自解析一次：父键的行只带父的 env，子键的行带合并后的 env。
		// 钩子取「第一个命中的键」即最长前缀（行按 key 长度倒序排列，见下方排序）。
		for _, bd := range b.Bindings {
			for _, k := range bindingMatchableKeys(bd) {
				r := ResolveBindings(k, b, s, ads)
				envStr := EnvPairsString(r.Env)
				for _, inj := range r.Inject {
					if !inj.PathUSable || inj.BinDir == "" {
						continue
					}
					picked = append(picked, row{k, inj.Tool, inj.BinDir, envStr})
				}
			}
		}
	}
	// 去重 + 稳定排序（cmpKey 长的在前，钩子取第一个命中即最长前缀）
	seen := map[string]bool{}
	sort.SliceStable(picked, func(i, j int) bool {
		if picked[i].key != picked[j].key {
			return len(picked[i].key) > len(picked[j].key)
		}
		return picked[i].tool < picked[j].tool
	})
	for _, p := range picked {
		// 契约 v2 硬校验（写侧；agent-A 的读侧按同一口径）：
		//  1) 数据行必须恰好 4 列制表符分隔，缺列/多列一律不生成；
		//  2) 第 1/3 列（明文路径）含 `%` 或换行 → 拒写该行并 warn（bat 的 % 展开语义穿不过去）；
		//  3) 第 4 列已 percent-encode，只再校一次不含制表/换行。
		if !genPlainSafe(p.key) {
			warns = append(warns, fmt.Sprintf("已拒写一行绑定表：目录键含 %% 或换行等 bat 不可安全展开的字符（%s）", p.key))
			continue
		}
		if !genPlainSafe(p.dir) {
			warns = append(warns, fmt.Sprintf("已拒写一行绑定表：版本目录含 %% 或换行等 bat 不可安全展开的字符（%s）", p.dir))
			continue
		}
		if !genToolSafe(p.tool) || !genEncodedSafe(p.env) {
			warns = append(warns, fmt.Sprintf("已拒写一行绑定表：工具名或 env 列非法（%s / %s）", p.tool, p.env))
			continue
		}
		line := strings.Join([]string{p.key, p.tool, p.dir, p.env}, genColSep)
		if strings.Count(line, genColSep) != genColCount-1 {
			warns = append(warns, "已拒写一行绑定表：列数不是恰好 4 列")
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		rows = append(rows, line)
	}
	sb := strings.Builder{}
	sb.WriteString(genTableHeader)
	sb.WriteString("\r\n")
	for _, r := range rows {
		sb.WriteString(r)
		sb.WriteString("\r\n")
	}
	return sb.String(), warns
}

// genPlainSafe 明文列（cmpKey / binDir）：不得含制表、换行、引号与 `%`
// —— `%` 在 bat 里是展开语义，穿不过去（契约 v2 第 2 条）。
func genPlainSafe(s string) bool {
	return s != "" && !strings.ContainsAny(s, "\t\r\n\"%")
}

// genToolSafe 工具列只允许标识符字符。
func genToolSafe(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// genEncodedSafe 第 4 列经过 percent-encode 后不应再含分隔类字符，这里只是二次兜底。
func genEncodedSafe(s string) bool {
	return !strings.ContainsAny(s, "\t\r\n\x00")
}

// WriteGenTable 落盘扁平表并回传拒写警告（供 CLI 打到 stderr）。
func WriteGenTable(b *Bindings, s *settings.Settings, ads []Adapter) ([]string, error) {
	text, warns := RenderGenTable(b, s, ads)
	return warns, writeFileAtomic(GenTablePath(), []byte(text))
}

func writeGenTableLocked(b *Bindings, s *settings.Settings, ads []Adapter) ([]string, error) {
	if s == nil {
		s, _ = settings.Load()
	}
	text, warns := RenderGenTable(b, s, ads)
	// 首行必须是 #version=1 且无 BOM（契约固定；写错会让 A 的 bat 把首行当数据行）
	if !strings.HasPrefix(text, genTableHeader) {
		warns = append(warns, "绑定表首行不是 #version=1，钩子将无法识别版本")
	}
	return warns, writeFileAtomic(GenTablePath(), []byte(text))
}

// genTableBudget 是「单次查表」的时间托底（§10 第 7 行：杀软拦截 / 解析超时 → 回退全局）。
// 测试里改小即可验证。
var genTableBudget = 250 * time.Millisecond

// GenTableLookup 读扁平表并返回命中 cwd 的注入项（供测试与 __bind-resolve 交叉验证）。
// 超过 genTableBudget 没读完（杀软拦截、磁盘挂起）→ 返回空并回传超时原因，
// 调用方据此「什么都不注入」= 本次用全局版本，命令绝不被阻塞（§10 首要原则）。
func GenTableLookup(cwd string) ([]BindingInjection, map[string]string) {
	inj, env, err := GenTableLookupErr(cwd)
	if err != nil {
		return nil, nil
	}
	return inj, env
}

// GenTableLookupErr 同 GenTableLookup，但把「超时 / 读失败」显式回传给调用方打印。
func GenTableLookupErr(cwd string) ([]BindingInjection, map[string]string, error) {
	type res struct {
		inj []BindingInjection
		env map[string]string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		inj, env, err := readGenTable(cwd)
		ch <- res{inj, env, err}
	}()
	select {
	case r := <-ch:
		return r.inj, r.env, r.err
	case <-time.After(genTableBudget):
		return nil, nil, fmt.Errorf("目录解析超过 %s 未响应（可能被安全软件拦截），本次已回退全局版本", genTableBudget)
	}
}

func readGenTable(cwd string) ([]BindingInjection, map[string]string, error) {
	data, err := os.ReadFile(GenTablePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil // 还没生成过：等价于「无绑定」
		}
		return nil, nil, err
	}
	cmp := CmpKey(cwd)
	var out []BindingInjection
	env := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, ln := range lines[1:] {
		cols := strings.Split(ln, genColSep)
		if len(cols) != genColCount {
			continue // 契约 v2：数据行恰好 4 列，缺列/多列不解析
		}
		if !hasBoundaryPrefix(strings.ToLower(cols[0]), cmp) {
			continue
		}
		inj := BindingInjection{Tool: cols[1], BinDir: cols[2], PathUSable: true}
		for k, v := range ParseEnvPairs(cols[3]) {
			env[k] = v
		}
		out = append(out, inj)
	}
	return out, env, nil
}

// —— 表的增删改（envs bind / unbind 用，§7.1）——————————————

// SetBindingTool 写入/覆盖某目录对某工具的绑定，返回该目录的条目。
// 目录按 §4.2 归一后存规范形态；UNC / 网络盘 / 裸盘根 / 不存在一律拒写（§10）。
func SetBindingTool(b *Bindings, cwd, tool, ver string, env map[string]string, note, source string) (*Binding, error) {
	if b == nil {
		return nil, W(ErrUsage, "绑定表未加载")
	}
	clean, err := ValidateBindingDir(cwd)
	if err != nil {
		return nil, err
	}
	id := strings.ToLower(strings.TrimSpace(tool))
	if ok, _, why := BindGate(id); !ok {
		return nil, W(ErrUsage, "%s", why)
	}
	if bad, why := LooksLikeConstraint(strings.TrimSpace(ver)); bad {
		return nil, W(ErrUsage, "%s", why)
	}
	// 先把 env 全量校验完再动表：任何一条非法都必须「整次写入不落地」，
	// 否则会留下半截条目（TestBindingsSetRejectsBadTargets 断言不污染）。
	for k, v := range env {
		if !safeEnvPair(strings.TrimSpace(k), v) {
			return nil, W(ErrUsage, "env 键 %q 非法（需为标识符）或值含换行/制表", k)
		}
	}
	var ent *Binding
	for i := range b.Bindings {
		if sameBindingPath(b.Bindings[i].Path, clean) {
			ent = &b.Bindings[i]
			break
		}
	}
	if ent == nil {
		b.Bindings = append(b.Bindings, Binding{Path: clean, Tools: map[string]string{}})
		ent = &b.Bindings[len(b.Bindings)-1]
	}
	if ent.Tools == nil {
		ent.Tools = map[string]string{}
	}
	ent.Tools[id] = strings.TrimSpace(ver)
	for k, v := range env {
		k = strings.TrimSpace(k)
		if !safeEnvPair(k, v) {
			continue // 已在前面的预校验挡掉；这里只是兜底，绝不再中途报错留半截
		}
		if ent.Env == nil {
			ent.Env = map[string]string{}
		}
		ent.Env[k] = v
	}
	if note != "" {
		ent.Note = note
	}
	if source != "" {
		ent.Source = source
	} else if ent.Source == "" {
		ent.Source = "manual"
	}
	ent.UpdatedAt = time.Now().Format(time.RFC3339)
	return ent, nil
}

// DeleteBindingTool 摘掉某目录对某工具的绑定；tool 为空 = 删整条目录绑定。
// 返回 (是否改动, 剩余工具数)。
func DeleteBindingTool(b *Bindings, cwd, tool string) (bool, int) {
	clean, err := ValidateBindingDir(cwd)
	if err != nil {
		return false, 0
	}
	for i := range b.Bindings {
		if !sameBindingPath(b.Bindings[i].Path, clean) {
			continue
		}
		if strings.TrimSpace(tool) == "" {
			b.Bindings = append(b.Bindings[:i], b.Bindings[i+1:]...)
			return true, 0 // 整条删除：该目录剩余工具数 0（第二个返回值语义是「剩余工具数」）
		}
		id := strings.ToLower(strings.TrimSpace(tool))
		if _, has := b.Bindings[i].Tools[id]; !has {
			return false, len(b.Bindings[i].Tools)
		}
		delete(b.Bindings[i].Tools, id)
		b.Bindings[i].UpdatedAt = time.Now().Format(time.RFC3339)
		if len(b.Bindings[i].Tools) == 0 {
			b.Bindings = append(b.Bindings[:i], b.Bindings[i+1:]...)
			return true, 0 // 最后一个工具也没了：整条删除，剩余 0
		}
		return true, len(b.Bindings[i].Tools)
	}
	return false, 0
}

func sameBindingPath(stored, clean string) bool {
	a, b := CmpKey(stored), strings.ToLower(clean)
	if a == "" || b == "" {
		return false
	}
	for _, k := range BindingKeys(stored) {
		if k == b {
			return true
		}
	}
	return a == b
}

// ValidateBindingDir 绑定目录的可写性校验：拒绝空、UNC / 网络盘、裸盘根、不存在。
// 与 ValidateStoreRoot 同一立场（§10「UNC / 网络盘拒写」）。
func ValidateBindingDir(dir string) (string, error) {
	raw := strings.TrimSpace(dir)
	if raw == "" {
		return "", W(ErrUsage, "目录不能为空")
	}
	if strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
		return "", W(ErrUsage, "不支持网络盘或 UNC 路径，请选本机磁盘目录（§5/§10）")
	}
	n := NormalizePath(raw)
	if n == "" {
		return "", W(ErrUsage, "路径无法解析：%s", raw)
	}
	if filepath.VolumeName(n) == "" {
		return "", W(ErrUsage, "请输入带盘符的绝对路径（当前：%s）", raw)
	}
	if IsBareRoot(strings.ToLower(n)) {
		return "", W(ErrUsage, "不要直接绑定盘根；请选择磁盘下的子目录")
	}
	if fi, err := os.Stat(n); err != nil || !fi.IsDir() {
		return "", W(ErrUsage, "目录不存在（可能已改名或在外接盘上）：%s", n)
	}
	return filepath.Clean(n), nil
}

// —— §5 档③/§1 裁决 6：上游标记探测 ————————————————

type projectMarkers struct {
	native  map[string]projectMarker // 工具 ID → 让位标记
	prompts []string                 // 只提示不生效的上游约定（§1 裁决 6）
	// pin / pinFile：.nvmrc 或 .node-version 里的 pin 值（walk-up 中最深一份优先）。
	// 只有 autoDetect=on 才可能据此注入（lead R6 裁决）；engines 是约束，永不进这里。
	pin     string
	pinFile string
	// webpackDep：package.json 的 devDependencies.webpack 原始字符串（§10 md4 行的判据）。
	webpackDep string
}

type projectMarker struct {
	File   string
	Detail string
}

// scanProjectMarkers 从 cwd 向上走（最多 maxDepth 层）找上游选型标记。
// node_modules 内的标记硬跳过（§2-L14：真实数据里 .nvmrc 全在 node_modules 里）。
// package.json 非 UTF-8 / 非法 JSON 一律不报错，只出一条提示（§10 GBK 行）。
func scanProjectMarkers(cwd string, maxDepth int) projectMarkers {
	out := projectMarkers{native: map[string]projectMarker{}}
	if maxDepth <= 0 {
		maxDepth = DefaultBindMaxDepth
	}
	dir := NormalizePath(cwd)
	if dir == "" || IsUNCKey(strings.ToLower(dir)) {
		return out
	}
	for depth := 0; depth < maxDepth; depth++ {
		low := strings.ToLower(dir) + `\`
		if strings.Contains(low, `\node_modules\`) || strings.HasSuffix(low, `\node_modules\`) {
			// §10「上游标记落在 node_modules 内 → 硬跳过」：本层不采信任一标记，
			// 但继续向上走，项目根自己的标记仍然要能提示（真实数据 §2-L14 里
			// 能搜到的 .nvmrc 全在 node_modules 里，忽略它们之后仍需看根）。
			if p := firstExisting(dir, ".nvmrc", ".node-version", "package.json", "go.mod", "global.json"); p != "" {
				out.prompts = append(out.prompts,
					fmt.Sprintf("已忽略 node_modules 内的 %s（§2-L14：依赖包自带的上游标记不作数）。", filepath.Base(p)))
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
			continue
		}
		// dotnet：global.json（§2-L13 实测读取）
		if _, has := out.native["dotnet"]; !has {
			if p := firstExisting(dir, "global.json"); p != "" {
				out.native["dotnet"] = projectMarker{"global.json", filepath.Dir(p)}
			}
		}
		// go：go.mod 声明 toolchain → 交还原生（§2-L13）
		if _, has := out.native["go"]; !has {
			if p := firstExisting(dir, "go.mod"); p != "" {
				if tc := goModToolchain(p); tc != "" {
					out.native["go"] = projectMarker{"go.mod", "toolchain " + tc}
				}
			}
		}
		// node 系包管理器：packageManager + corepack（§2-L13 实测生效）
		if _, has := out.native["pnpm"]; !has {
			if p := firstExisting(dir, "package.json"); p != "" {
				pm, engines, webpackDep, readable, utf8ok := readPackageJSON(p)
				if webpackDep != "" && out.webpackDep == "" {
					out.webpackDep = webpackDep
				}
				if !readable {
					out.prompts = append(out.prompts,
						fmt.Sprintf("项目 %s 不是有效 JSON，未能读取 engines/packageManager 字段。", filepath.Base(p)))
				} else if !utf8ok {
					out.prompts = append(out.prompts,
						"项目 package.json 非 UTF-8，未能读取 engines 字段（§10 GBK 托底）。")
				} else {
					if pm != "" {
						id := "pnpm"
						if strings.HasPrefix(pm, "yarn") {
							id = "yarn"
						}
						out.native[id] = projectMarker{"package.json", "packageManager=" + pm}
					}
					if engines != "" {
						if bad, _ := LooksLikeConstraint(engines); bad {
							out.prompts = append(out.prompts, fmt.Sprintf(
								"package.json 的 engines.node=%q：这是约束，不能定位具体版本，未生效（§1 裁决 6）。要固定版本请执行 envs bind node <ver>。", engines))
						} else {
							out.prompts = append(out.prompts, fmt.Sprintf(
								"package.json 的 engines.node=%q：默认只提示不生效（§1 裁决 6）。要采纳为绑定：envs bind node %s", engines, engines))
						}
					}
				}
			}
		}
		// python：venv（pyvenv.cfg）/ uv（.python-version）（§2-L13）
		if _, has := out.native["python"]; !has {
			if p := firstExisting(dir, "pyvenv.cfg"); p != "" {
				out.native["python"] = projectMarker{"pyvenv.cfg", filepath.Dir(p) + " 已在 PATH 首位决定 python"}
			} else if p := firstExisting(dir, ".python-version"); p != "" {
				out.native["python"] = projectMarker{".python-version", "由 venv/uv 读取（§2-L13）"}
			}
		}
		// jvm wrapper：gradlew / mvnw 自管发行版（§5 档③）
		if _, has := out.native["gradle"]; !has {
			if p := firstExisting(dir, "gradlew", "gradlew.bat"); p != "" {
				out.native["gradle"] = projectMarker{filepath.Base(p), "wrapper 自管 Gradle 发行版"}
			}
		}
		if _, has := out.native["maven"]; !has {
			if p := firstExisting(dir, "mvnw", "mvnw.cmd"); p != "" {
				out.native["maven"] = projectMarker{filepath.Base(p), "wrapper 自管 Maven 发行版"}
			}
		}
		// 上游约定（.nvmrc / .node-version）：只提示不生效（§1 裁决 6）
		promptMarkers(&out, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

// promptMarkers 收集 .nvmrc / .node-version 提示（不改变注入结果）。
func promptMarkers(out *projectMarkers, dir string) {
	for _, name := range []string{".nvmrc", ".node-version"} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil || !utf8.Valid(data) {
			continue
		}
		v := strings.TrimSpace(strings.TrimSuffix(string(data), "\ufeff"))
		if v == "" {
			continue
		}
		// 最深的一份 pin 优先（walk-up 由内向外，先到先得）：autoDetect=on 才用它注入
		if out.pin == "" {
			out.pin = v
			out.pinFile = p
		}
		out.prompts = append(out.prompts, fmt.Sprintf(
			"发现 %s（内容 %q）：上游约定默认只提示不生效（§1 裁决 6）。要采纳为绑定：envs bind node %s", p, v, v))
	}
}

func firstExisting(dir string, names ...string) string {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

func goModToolchain(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) {
		return ""
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if rest, ok := strings.CutPrefix(ln, "toolchain "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// readPackageJSON 返回 packageManager / engines.node / devDependencies.webpack / 是否可读 / 是否 UTF-8。
// 任何失败都不返回 error —— 调用方只把它当提示（§10 GBK 行）。
func readPackageJSON(path string) (pm, engines, webpackDep string, readable, utf8ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", false, false
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return "", "", "", true, false
	}
	var v struct {
		PackageManager string            `json:"packageManager"`
		Engines        map[string]string `json:"engines"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return "", "", "", true, true
	}
	// webpack 只从 devDependencies 取原始字符串：不解析 semver 范围（§4.3 不做范围）、
	// 不进 node_modules、不装依赖（lead R6 裁决的最省形态）。字段名不带 tag，
	// 靠 encoding/json 的大小写不敏感匹配 devDependencies。
	var dev struct {
		DevDependencies map[string]string
	}
	_ = json.Unmarshal(data, &dev)
	return strings.TrimSpace(v.PackageManager), strings.TrimSpace(v.Engines["node"]),
		strings.TrimSpace(dev.DevDependencies["webpack"]), true, true
}

// —— §7.3 doctor 内核件（供 agent-A 的 doctor.go 追加调用）——

// DoctorBindings 给出绑定内核负责的体检项：
// 第 2 项绑定表总览、第 4 项 shim 自测、第 5 项双形态 shim 缺失、第 7 项原子写、第 8 项锁文件漂移。
func DoctorBindings(ads []Adapter, s *settings.Settings) []Check {
	b, warns := LoadBindings()
	var out []Check

	// 第 2 项：总览 N 条 / X 未装 / Y 目录不存在 / Z 冲突（逐条给修法）
	notInstalled, missingDir, conflict := 0, 0, 0
	var hints []string
	for _, bd := range b.Bindings {
		if _, err := os.Stat(NormalizePath(bd.Path)); err != nil {
			missingDir++
			hints = append(hints, fmt.Sprintf("目录不存在：%s（envs unbind --cwd %q 删除，或改路径后重新 bind）", bd.Path, bd.Path))
			continue
		}
		for tool, expr := range bd.Tools {
			if ok, _, _ := BindGate(tool); !ok {
				continue
			}
			host := bindHostTool[tool]
			if host == "" {
				host = tool
			}
			ad := adapterOf(ads, host)
			if ad == nil {
				continue
			}
			if got := ResolveBindings(bd.Path, b, s, ads); got != nil {
				for _, sk := range got.Skips {
					if sk.Tool != tool {
						continue
					}
					switch sk.Kind {
					case "not-installed", "missing-dir":
						notInstalled++
						hints = append(hints, fmt.Sprintf("%s 的 %s %s 未安装：%s", bd.Path, tool, expr, sk.Reason))
					case "poisoned":
						conflict++
						hints = append(hints, fmt.Sprintf("%s 的 %s %s 目录不被信任：%s", bd.Path, tool, expr, sk.Reason))
					}
				}
			}
		}
	}
	status := "ok"
	if missingDir+notInstalled+conflict > 0 || len(warns) > 0 {
		status = "warn"
	}
	detail := fmt.Sprintf("%d 条 / %d 未装 / %d 目录不存在 / %d 冲突（enabled=%v, autoDetect=%s）",
		b.Count(), notInstalled, missingDir, conflict, b.Enabled, b.AutoDetect)
	if len(warns) > 0 {
		detail += " · " + warns[0]
		status = "bad"
	}
	out = append(out, Check{"目录绑定表", status, detail})
	for _, h := range hints {
		out = append(out, Check{"目录绑定·修法", "warn", h})
	}

	// 第 4 项：以某个绑定目录为 cwd 解析一次，按 **execPath 口径** 与 PATH 实际胜出对比
	// （lead R12：不看版本号，版本号可能恰好相同 —— 本机全局就是 v16.20.2；
	//   样例见 agent-A 的 shim_cmd_test.go 真机结果：E:\nvm\v22.22.0\node.exe vs C:\nvm4w\nodejs\node.exe）。
	for _, bd := range b.Bindings {
		r := ResolveBindings(bd.Path, b, s, ads)
		for _, inj := range r.Inject {
			if !inj.PathUSable {
				continue
			}
			ad := adapterOf(ads, bindHostTool[inj.Tool])
			if ad == nil {
				ad = adapterOf(ads, inj.Tool)
			}
			if ad == nil {
				continue
			}
			exeName := PathExeName(ad)
			if exeName == "" {
				continue
			}
			boundExe := filepath.Join(inj.BinDir, exeName)
			actual := WhichResolved(exeName)
			who := filepath.Base(bd.Path)
			switch {
			case actual == "":
				out = append(out, Check{"绑定应执行 vs 实际胜出", "info",
					fmt.Sprintf("%s 绑定 %s=%s → %s；PATH 上目前叫不出 %s（未装或未进 PATH）",
						who, inj.Tool, inj.Version, boundExe, exeName)})
			case strings.EqualFold(filepath.Clean(actual), filepath.Clean(boundExe)):
				out = append(out, Check{"绑定应执行 vs 实际胜出", "ok",
					fmt.Sprintf("%s 绑定 %s=%s，PATH 实际胜出就是它：%s", who, inj.Tool, inj.Version, boundExe)})
			default:
				cur, _, _ := CurrentInstall(ad, s)
				act := ""
				if cur != nil {
					act = "，激活版本 " + cur.Version + " 也抢不过它"
				}
				out = append(out, Check{"绑定应执行 vs 实际胜出", "warn",
					fmt.Sprintf("%s 绑定 %s=%s 应执行 %s，实际胜出 %s%s → 层 2 shim 未生效（被上位抢位，§2-L1/L2）；"+
						"层 1 会话钩子（envs init）与层 3 envs run 不受影响（它们按注入后的 PATH 查找，§7.3-4 execPath 口径）",
						who, inj.Tool, inj.Version, boundExe, actual, act)})
			}
		}
		break // 只测第一条有效绑定，doctor 不该为全表跑解析
	}

	// 第 5 项：双形态 shim 缺失（层 2 归 agent-A；这里只做只读检查）
	var noPosix []string
	for _, ad := range ads {
		jr, ok := ad.(JSRunner)
		if !ok {
			continue
		}
		for name := range jr.Shims() {
			if _, err := os.Stat(filepath.Join(settings.ShimsDir(s), name)); err != nil {
				noPosix = append(noPosix, name)
			}
		}
	}
	if len(noPosix) > 0 {
		sort.Strings(noPosix)
		out = append(out, Check{"无扩展名 shim", "warn",
			fmt.Sprintf("缺 %d 个 POSIX 形态入口（%s）：Git Bash 里不会命中（§2-L7）", len(noPosix), strings.Join(noPosix, ", "))})
	} else {
		out = append(out, Check{"无扩展名 shim", "ok", "双形态入口齐备"})
	}

	// 第 7 项：bindings.json 可原子写校验
	if err := CheckBindingsWritable(); err != nil {
		out = append(out, Check{"绑定表原子写", "bad", fmt.Sprintf("无法在 home 下做临时文件+重命名：%v；绑定命令会退 5", err)})
	} else {
		out = append(out, Check{"绑定表原子写", "ok", BindingsPath()})
	}

	// 第 8 项：锁文件与解析到的 node 大版本漂移
	for _, bd := range b.Bindings {
		r := ResolveBindings(bd.Path, b, s, ads)
		if w := LockfileDriftWarning(bd.Path, r); w != "" {
			out = append(out, Check{"锁文件漂移", "warn", w})
		}
	}
	return out
}

// LockfileDriftWarning 检查 node_modules/.package-lock.json 的 lockfileVersion
// 是否与解析到的 node 大版本相配（§7.3-8 / §2-L14）。不匹配只 warn，绝不改文件。
func LockfileDriftWarning(dir string, r *BindingResolve) string {
	nodeVer := ""
	for _, inj := range r.Inject {
		if bindHostTool[inj.Tool] == "npm" || inj.Tool == "node" {
			nodeVer = inj.Version
			break
		}
	}
	if nodeVer == "" {
		return ""
	}
	p := filepath.Join(NormalizePath(dir), "node_modules", ".package-lock.json")
	data, err := os.ReadFile(p)
	if err != nil || !json.Valid(data) {
		return "" // 没装依赖 / 读不动：不报（§10 读取失败不报错）
	}
	var v struct {
		LockfileVersion int `json:"lockfileVersion"`
	}
	if err := json.Unmarshal(data, &v); err != nil || v.LockfileVersion == 0 {
		return ""
	}
	major := 0
	if _, err := fmt.Sscanf(strings.TrimPrefix(NormVersion(nodeVer), ""), "%d", &major); err != nil {
		return ""
	}
	npmOfNode := map[int]string{14: "6", 16: "8", 18: "9", 20: "10", 22: "10"}[major]
	if npmOfNode == "" {
		return ""
	}
	// lockfileVersion：1=npm≤6，2=npm7/8，3=npm9/10（§2-L14 实测）。
	// 只列真正会写这个版本的 npm 大版本，不臆造单一数字。
	writerOf := map[int]string{1: "npm ≤6", 2: "npm 7/8", 3: "npm 9/10"}
	writer := writerOf[v.LockfileVersion]
	if writer == "" {
		return "" // 认不出的锁文件版本不乱猜
	}
	wantMax := map[string]int{"6": 2, "8": 2, "9": 3, "10": 3}[npmOfNode]
	if wantMax == 0 || v.LockfileVersion <= wantMax {
		return ""
	}
	return fmt.Sprintf("依赖由 %s 生成（lockfile v%d），当前绑定 npm %s，原生模块与锁文件可能需重装（§10）。",
		writer, v.LockfileVersion, npmOfNode)
}

// —— 展示辅助 ——————————————————————————————————

// BindingRow 一条展开的绑定行（GUI / envs bindings --json 共用）。
type BindingRow struct {
	Path     string            `json:"path"`
	Tools    map[string]string `json:"tools"`
	Env      map[string]string `json:"env,omitempty"`
	Note     string            `json:"note,omitempty"`
	Source   string            `json:"source,omitempty"`
	Resolved map[string]string `json:"resolved,omitempty"` // 工具 → 实际解析到的版本
	Status   string            `json:"status"`             // ok / warn / bad
	Reasons  []string          `json:"reasons,omitempty"`
}

// BindingRows 给 GUI 与 envs bindings 用的行视图（含状态灯与原因）。
func BindingRows(b *Bindings, s *settings.Settings, ads []Adapter) []BindingRow {
	if b == nil {
		return nil
	}
	var out []BindingRow
	for _, bd := range b.Bindings {
		r := &BindingResolve{}
		if b.Enabled {
			r = ResolveBindings(bd.Path, b, s, ads)
		}
		row := BindingRow{Path: bd.Path, Tools: bd.Tools, Env: bd.Env, Note: bd.Note, Source: bd.Source,
			Status: "ok", Resolved: map[string]string{}}
		if _, err := os.Stat(NormalizePath(bd.Path)); err != nil {
			row.Status = "bad"
			row.Reasons = append(row.Reasons, "目录不存在（可能已改名）。绑定仍在表中，修好路径即恢复。")
		}
		for _, sk := range r.Skips {
			if sk.Kind == "disabled" {
				continue
			}
			if row.Status == "ok" {
				row.Status = "warn"
			}
			row.Reasons = append(row.Reasons, fmt.Sprintf("%s: %s", sk.Tool, sk.Reason))
		}
		for _, inj := range r.Inject {
			row.Resolved[inj.Tool] = inj.Version
		}
		out = append(out, row)
	}
	return out
}

// webpackMd4Warning 是 §10「webpack4 + node≥18 的 md4」那一行。
// 判据刻意保持最省：只看 package.json 的 devDependencies.webpack 原始字符串前缀（4. / ^4），
// 不装依赖、不读 node_modules、不解析 semver 范围（§4.3 不做范围）。
func webpackMd4Warning(r *BindingResolve, m projectMarkers) string {
	if r == nil || m.webpackDep == "" {
		return ""
	}
	wp := strings.TrimLeft(strings.TrimPrefix(m.webpackDep, "v"), "^~>=< ")
	if !strings.HasPrefix(wp, "4.") && wp != "4" {
		return ""
	}
	nodeVer := ""
	for _, x := range r.Inject {
		if x.Tool == "node" {
			nodeVer = x.Version
			break
		}
	}
	if nodeVer == "" {
		return ""
	}
	parts := splitVer(nodeVer)
	if len(parts) == 0 || parts[0] < 18 {
		return ""
	}
	return fmt.Sprintf("该项目使用 webpack 4（devDependencies.webpack=%q），在 node %s 下会撞 ERR_OSSL_EVP_UNSUPPORTED（md4 不可用，§2-L14）。"+
		"在绑定里加 env 覆盖即可：envs bind node %s --env \"NODE_OPTIONS=--openssl-legacy-provider\"", m.webpackDep, nodeVer, nodeVer)
}

// InstallMissingHook 是 --install-if-missing 的实现注入点（§10 第 1 行的唯一例外开关）。
// 由 CLI 赋成真正的「ResolveRemote + InstallPipeline」编排；core 自己不碰网络。
// 做成包级变量是为了单能用记录桩断言「不传 flag 时绝不触发下载」。
var InstallMissingHook func(ctx context.Context, tool, expr string) error

// MaybeInstallMissing 仅在 flag=true 时对 not-installed / missing-dir 的条目调用 hook。
// 默认关：不传 flag 时一个字节都不会发出去（返回 nil, nil）。
func MaybeInstallMissing(ctx context.Context, r *BindingResolve, flag bool) ([][2]string, error) {
	if r == nil || !flag || InstallMissingHook == nil {
		return nil, nil
	}
	var tried [][2]string
	for _, sk := range r.Skips {
		if sk.Kind != "not-installed" && sk.Kind != "missing-dir" {
			continue
		}
		host := BindHostOf(sk.Tool)
		tried = append(tried, [2]string{host, sk.Expr})
		if err := InstallMissingHook(ctx, host, sk.Expr); err != nil {
			return tried, err
		}
	}
	return tried, nil
}

// GenTableRows 数一下扁平面里的数据行数（首行 #version=1 与注释/空行不算）。
func GenTableRows() int {
	data, err := os.ReadFile(GenTablePath())
	if err != nil {
		return 0
	}
	rows := 0
	for i, ln := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if i == 0 || strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		rows++
	}
	return rows
}

// Layer2GuardNotice 是层 2 行数护栏的用户可见提示（§11 成本护栏，数字与措辞与
// agent-A 的 shims.go::shimGenRowGuard / doctor.go::layer2GuardCheck 同一口径 ——
// 直接引用同一个常量，避免两处数字漂移）。跨过那条线时才返回非空。
func Layer2GuardNotice(rows int) string {
	if rows <= shimGenRowGuard {
		return ""
	}
	return fmt.Sprintf("绑定表 %d 行 > %d 行成本护栏：shim（层 2 兜底）只认**前 %d 行**"+
		"（envs bind 按键长倒序写表，前 %d 行就是最深/最具体的那几条），其余条目在层 2 不生效；"+
		"要全覆盖请用层 1 会话钩子（envs init <shell>）或显式 envs run —— 二者没有这个上限",
		rows, shimGenRowGuard, shimGenRowGuard, shimGenRowGuard)
}

// ExplainText 把一次解析写成给人看的段落（envs resolve --explain / bind why）。
func (r *BindingResolve) ExplainText() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "解析目录：%s\n", r.Cwd)
	fmt.Fprintf(&sb, "cmpKey  ：%s\n", r.CmpKey)
	if BindingsDisabled() {
		sb.WriteString("ENVS_DISABLE=1 → 本进程不解析目录绑定。\n")
		return sb.String()
	}
	if !r.Enabled {
		sb.WriteString("总开关 enabled=false → 全部用全局版本（envs bind enable 开启）。\n")
		return sb.String()
	}
	if len(r.Hits) == 0 {
		sb.WriteString("命中    ：无（没有任何绑定覆盖该目录）\n")
	} else {
		sb.WriteString("命中链  ：（由浅到深）\n")
		for _, h := range r.Hits {
			fmt.Fprintf(&sb, "  · %s\n", h.Path)
		}
	}
	if len(r.Inject) > 0 {
		sb.WriteString("注入    ：\n")
		for _, x := range r.Inject {
			tail := ""
			if !x.PathUSable {
				tail = "（不进 PATH：随 node 目录自带入口）"
			}
			fmt.Fprintf(&sb, "  · %-7s %s → %s [%s]%s\n", x.Tool, x.Expr, x.Version, x.Source, tail)
		}
	}
	if len(r.Env) > 0 {
		sb.WriteString("env 覆盖：\n")
		for _, k := range sortedKeys(r.Env) {
			fmt.Fprintf(&sb, "  · %s=%s\n", k, r.Env[k])
		}
	}
	if len(r.Skips) > 0 {
		sb.WriteString("未生效  ：\n")
		for _, s := range r.Skips {
			fmt.Fprintf(&sb, "  · [%s] %s %s → %s\n", s.Kind, s.Tool, s.Expr, s.Reason)
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&sb, "提示    ：%s\n", n)
	}
	for _, w := range r.Warns {
		fmt.Fprintf(&sb, "警告    ：%s\n", w)
	}
	return sb.String()
}
