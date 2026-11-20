// envs —— EnvSwitch CLI。与 GUI 共享 internal/core，行为单一事实源。
// 退出码：0 成功 / 1 未知 / 2 参数 / 3 网络 / 4 校验 / 5 权限 / 6 版本歧义。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/MUHUIX/envswitch/internal/core"
	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
	"github.com/MUHUIX/envswitch/internal/tools"
)

// version 供 -X main.version 旧注入兼容；显示统一走 core.BuildVersion。
var version = "dev"

func mainVersion() string {
	if v := core.BuildVersion(); v != "dev" {
		return v
	}
	return version
}

var (
	flagJSON  bool
	settings_ *settings.Settings
)

func main() {
	root := &cobra.Command{
		Use:           "envs",
		Short:         "EnvSwitch —— Windows 开发环境版本切换台",
		Version:       mainVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			s, err := settings.Load()
			if err != nil {
				return err
			}
			settings_ = s
			// 隐藏命令 __bind-resolve 被 cmd AutoRun / shim 高频调用：
			// 它只查预生成的扁平面，不得顺手重写 shim（§7.1：EnsureShims 按命令名跳过）。
			if cmd.Name() != core.BindResolveHiddenCmd {
				_ = core.EnsureShims(s, tools.All()) // shim 模板升级自愈
			}
			return nil
		},
	}
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "机器可读输出")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	root.AddCommand(
		statusCmd(), listCmd(), useCmd(), installCmd(ctx), removeCmd(),
		whichCmd(), doctorCmd(), autostartCmd(), configCmd(), versionCmd(), pathCmd(),
		cleanCmd(), guiCmd(), selfCmd(ctx),
		// —— 目录级版本绑定（方案 §6.1 / §6.3 / §7.1）——
		bindCmd(), unbindCmd(), bindingsCmd(), resolveCmd(), runCmd(ctx), shellCmd(), initCmd(),
		bindResolveCmd(),
	)
	if err := root.Execute(); err != nil {
		var pe *exec.Error
		if errors.As(err, &pe) {
			err = core.W(core.ErrUsage, "%v", err)
		}
		if code := exitCodeOf(err); code != 0 {
			// §7.1 --json 契约：stdout 只有合法 JSON（失败也要有），诊断一律走 stderr
			if flagJSON {
				outJSON(map[string]any{"error": err.Error(), "code": code})
			}
			fmt.Fprintln(os.Stderr, "错误:", err)
			os.Exit(code)
		}
	}
}

func exitCodeOf(err error) int { return core.ExitCode(err) }

// —— helpers ——

func mustTool(id string) core.Adapter {
	ad := tools.ByID(id)
	if ad == nil {
		var names []string
		for _, a := range tools.All() {
			names = append(names, a.ID())
		}
		exit(core.W(core.ErrUsage, "未知工具 %q，可选: %s", id, strings.Join(names, " / ")))
	}
	return ad
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(core.ExitCode(err))
}

func outJSON(v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

func tw() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
}

// parseToolVer 支持 `pnpm@9.12.3` 与两参数形式。
func parseToolVer(args []string) (tool, ver string) {
	if len(args) >= 2 {
		return args[0], args[1]
	}
	if t, v, ok := strings.Cut(args[0], "@"); ok && t != "" {
		return t, v
	}
	return args[0], ""
}

// —— version ——

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "输出版本",
		Run: func(cmd *cobra.Command, args []string) {
			if flagJSON {
				outJSON(map[string]string{"version": mainVersion(), "go": runtime.Version()})
			} else {
				fmt.Println("envs", mainVersion(), "/ built with", runtime.Version())
			}
		},
	}
}

// —— status ——

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "各工具当前激活版本与来源",
		RunE: func(cmd *cobra.Command, args []string) error {
			type item struct {
				Tool     string `json:"tool"`
				Version  string `json:"version"`
				Source   string `json:"source"`
				Path     string `json:"path"`
				Resolved string `json:"resolvedPath"`
			}
			var items []item
			w := tw()
			fmt.Fprintln(w, "工具\t激活版本\t来源\t激活链接目标\tPATH 实际胜出")
			for _, ad := range tools.All() {
				cur, target, _ := core.CurrentInstall(ad, settings_)
				resolved := core.WhichResolved(core.PathExeName(ad))
				if cur == nil {
					fmt.Fprintf(w, "%s\t未激活\t-\t-\t%s\n", ad.ID(), dash(resolved))
					items = append(items, item{Tool: ad.ID(), Resolved: resolved})
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", ad.ID(), cur.Version, cur.Source, dash(target), dash(resolved))
				items = append(items, item{Tool: ad.ID(), Version: cur.Version, Source: cur.Source, Path: target, Resolved: resolved})
			}
			w.Flush()
			if flagJSON {
				outJSON(items)
			}
			return nil
		},
	}
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// —— list ——

func listCmd() *cobra.Command {
	var remote bool
	var filter string
	c := &cobra.Command{
		Use:   "list <tool>",
		Short: "列出已安装版本；--remote 列远程可装",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ad := mustTool(args[0])
			vers := core.ListInstalls(ad, settings_)
			if remote {
				rv, stale, err := core.RemoteCached(cmd.Context(), ad, settings_)
				if err != nil {
					return err
				}
				w := tw()
				fmt.Fprintln(w, "版本\t标记\t日期\t大小\t状态")
				for _, v := range rv {
					if filter != "" && !strings.HasPrefix(v.Version, strings.TrimPrefix(filter, "v")) {
						continue
					}
					status := ""
					for _, iv := range vers {
						if iv.Version == v.Version {
							status = "已装"
						}
					}
					label := v.Label
					if v.Codename != "" {
						label += " " + v.Codename
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", v.Version, label, dash(v.Date), core.HumanSize(v.Size), status)
				}
				w.Flush()
				if stale {
					fmt.Println("（离线：显示的是缓存索引）")
				}
				return nil
			}
			if len(vers) == 0 {
				fmt.Printf("%s：本机没有版本，用 envs install %s <ver> 安装\n", ad.ID(), ad.ID())
				return nil
			}
			cur, _, _ := core.CurrentInstall(ad, settings_)
			w := tw()
			fmt.Fprintln(w, "版本\t来源\t大小\t状态")
			for _, iv := range vers {
				st := ""
				if cur != nil && cur.Version == iv.Version {
					st = "◀ 当前"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", iv.Version, iv.Source, core.HumanSize(iv.Size), st)
			}
			w.Flush()
			if flagJSON {
				outJSON(vers)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&remote, "remote", false, "列远程可装版本")
	c.Flags().StringVar(&filter, "filter", "", "版本前缀过滤，如 22")
	return c
}

// —— use ——

func useCmd() *cobra.Command {
	var noPath, here bool
	c := &cobra.Command{
		Use:   "use <tool> <ver|latest|前缀>",
		Short: "切换激活版本（只重指激活链接，毫秒级）；--here 改为只绑当前目录",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool, ver := parseToolVer(args)
			if ver == "" {
				ver = "latest"
			}
			// §7.1 use --here：不碰全局激活链接，只写当前目录的绑定
			if here {
				b, warns := core.LoadBindings()
				bindWarnToStderr(warns)
				dir, _ := os.Getwd()
				ent, err := core.SetBindingTool(b, dir, tool, ver, nil, "", "cli-use-here")
				if err != nil {
					return err
				}
				if err := saveBindingsCmd(b, false); err != nil {
					return err
				}
				if flagJSON {
					outJSONOnly(map[string]any{"path": ent.Path, "tools": ent.Tools, "enabled": b.Enabled, "globalSwitched": false})
					return nil
				}
				fmt.Printf("已把 %s 绑定到 %s %s（全局激活版本未改动）\n", ent.Path, tool, ver)
				if !b.Enabled {
					fmt.Println("总开关还没开：envs bind enable")
				}
				return nil
			}
			ad := mustTool(tool)
			got, pathChanged, err := core.UseByName(settings_, ad, ver, !noPath)
			if err != nil {
				return err
			}
			if pathChanged {
				fmt.Println("首次启用：已写入用户 PATH（新开的终端生效）")
			}
			fmt.Printf("%s → %s\n", tool, got)
			fmt.Println("新开的终端立即使用新版本；已打开的终端保持原环境。")
			return nil
		},
	}
	c.Flags().BoolVar(&noPath, "no-path", false, "只切激活链接，不写 PATH（调试/手动管理场景）")
	c.Flags().BoolVar(&here, "here", false, "不改全局：把该版本绑定到当前目录（§7.1）")
	return c
}

// —— install ——

func installCmd(ctx context.Context) *cobra.Command {
	return &cobra.Command{
		Use:   "install <tool>@<ver> | install <tool> <ver|latest>",
		Short: "安装版本（下载→校验→解包→冒烟）；无激活版本时装完自动切换",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool, ver := parseToolVer(args)
			if ver == "" {
				ver = "latest"
			}
			ad := mustTool(tool)
			target, stale, err := core.ResolveRemote(ctx, settings_, ad, ver)
			if err != nil {
				return err
			}
			if stale {
				fmt.Fprintln(os.Stderr, "提示：远程索引来自缓存")
			}
			prog := func(done, total int64, speed float64) {
				if flagJSON {
					return
				}
				pct := ""
				if total > 0 {
					pct = fmt.Sprintf(" %2.0f%%", float64(done)/float64(total)*100)
				}
				fmt.Fprintf(os.Stderr, "\r下载 %s → %s%s (%s/s)", target.Archive, core.HumanSize(done), pct, core.HumanSize(int64(speed)))
			}
			if err := core.InstallPipeline(ctx, settings_, ad, *target, prog); err != nil {
				fmt.Fprintln(os.Stderr)
				return err
			}
			fmt.Fprintf(os.Stderr, "\r%s %s 安装完成（冒烟通过）\n", tool, target.Version)

			// 无激活版本 → 自动切上（复用 use 编排，含 PATH 首写）
			if cur, _, _ := core.CurrentInstall(ad, settings_); cur == nil {
				if _, _, err := core.UseByName(settings_, ad, target.Version, true); err == nil {
					fmt.Printf("已自动激活 %s %s\n", tool, target.Version)
				}
			}
			return nil
		},
	}
}

// —— remove ——

func removeCmd() *cobra.Command {
	var yes, cascade bool
	c := &cobra.Command{
		Use:   "remove <tool> <ver>",
		Short: "卸载版本库中的版本",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool, ver := parseToolVer(args)
			if ver == "" {
				return core.W(core.ErrUsage, "remove 需要明确版本号")
			}
			ad := mustTool(tool)
			if !yes {
				fmt.Printf("确认删除 %s %s？[y/N] ", tool, ver)
				var ans string
				fmt.Scanln(&ans)
				if !strings.EqualFold(ans, "y") {
					fmt.Println("已取消")
					return nil
				}
			}
			err := core.RemoveVersion(settings_, ad, ver, cascade)
			if err != nil {
				if strings.Contains(err.Error(), "已自动切到") {
					fmt.Println(err)
					return nil
				}
				return err
			}
			fmt.Printf("%s %s 已卸载\n", tool, ver)
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "跳过确认")
	c.Flags().BoolVar(&cascade, "cascade", false, "允许删除当前激活版本（自动切到剩余最新）")
	return c
}

// —— which ——

func whichCmd() *cobra.Command {
	var cwd string
	c := &cobra.Command{
		Use:   "which <tool>",
		Short: "解析当前实际生效的可执行文件路径；--cwd 并排显示「PATH 实际胜出」与「绑定应得」",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ad := mustTool(args[0])
			exe := core.PathExeName(ad)
			got := core.WhichResolved(exe)
			// §6.3：绑定应得的那一条（只在给了 --cwd 或启用绑定且命中时才有）
			r := core.ResolveBindings(cwd, nil, settings_, tools.All())
			type whichOut struct {
				Tool       string `json:"tool"`
				Exe        string `json:"exe"`
				OnPATH     string `json:"onPath"`
				Bound      string `json:"bound"`
				BoundVer   string `json:"boundVersion"`
				Same       bool   `json:"same"`
				Cwd        string `json:"cwd"`
				Reason     string `json:"reason,omitempty"`
				OwnedByEnv bool   `json:"ownedByEnvswitch"`
			}
			o := whichOut{Tool: args[0], Exe: exe, OnPATH: got, Cwd: r.Cwd, OwnedByEnv: settings.IsOwnedPath(filepath.Dir(got))}
			for _, inj := range r.Inject {
				if inj.Tool != args[0] && core.BindHostOf(inj.Tool) != args[0] {
					continue
				}
				o.Bound = filepath.Join(inj.BinDir, exe)
				o.BoundVer = inj.Version
				break
			}
			o.Same = o.Bound != "" && strings.EqualFold(filepath.Clean(o.Bound), filepath.Clean(got))
			if len(r.Skips) > 0 {
				o.Reason = r.Skips[0].Reason
			}
			if flagJSON {
				outJSONOnly(o)
				return nil
			}
			if got == "" {
				fmt.Printf("%s：PATH 上没有 %s\n", args[0], exe)
			} else {
				fmt.Printf("PATH 实际胜出：%s\n", got)
				if !o.OwnedByEnv {
					fmt.Println("（来源不是 EnvSwitch —— 用 envs doctor 查看 PATH 优先级）")
				}
			}
			if o.Bound != "" {
				mark := "≠"
				if o.Same {
					mark = "="
				}
				fmt.Printf("绑定应得    ：%s（%s %s）%s 实际胜出\n", o.Bound, args[0], o.BoundVer, mark)
				if !o.Same {
					fmt.Println("→ 会话注入（envs init）或 envs run 才会用上绑定；已打开的终端不变。")
				}
			} else if cwd != "" && o.Reason != "" {
				fmt.Println("绑定未生效  ：" + o.Reason)
			}
			return nil
		},
	}
	c.Flags().StringVar(&cwd, "cwd", "", "按该目录解析绑定（默认当前目录）")
	return c
}

// —— doctor ——

func doctorCmd() *cobra.Command {
	var bundle, fix, undo bool
	c := &cobra.Command{
		Use:   "doctor",
		Short: "体检：PATH 优先级、激活链接、写权限、共存、WebView2",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := tw()
			// --undo：把上一次 --fix/GUI 修复写进的用户 PATH 还原（快照在 home\doctor-fix.json）
			if undo {
				notes, err := core.UndoDoctorFix(settings_)
				for _, n := range notes {
					fmt.Println(n)
				}
				if err != nil {
					return err
				}
				fmt.Println()
			}
			// --fix：只修 doctor 里标了「可修复」的那几项（EnvSwitch 自有 PATH 条目 +
			// 激活链接重指 + 托管变量校准），修前自动快照，随时 --undo 撤回。
			if fix {
				notes, err := core.DoctorFix(settings_, tools.All(), "")
				for _, n := range notes {
					fmt.Println(n)
				}
				if err != nil {
					return err
				}
				fmt.Println()
			}
			checks := core.Doctor(tools.All(), settings_)
			for _, ch := range checks {
				mark := map[string]string{"ok": "✓", "warn": "!", "bad": "✗", "info": "·"}[ch.Status]
				hint := ""
				if (ch.Status == "warn" || ch.Status == "bad") && core.FixableFor(ch.Name, tools.All(), settings_) {
					hint = "（可修复：envs doctor --fix）"
				} else if ch.Status == "warn" || ch.Status == "bad" {
					hint = "（需手动：见说明）"
				}
				fmt.Fprintf(w, "%s\t%s\t%s%s\n", mark, ch.Name, ch.Detail, hint)
			}
			w.Flush()
			if flagJSON {
				outJSON(checks)
			}
			if bundle {
				p, err := core.BundleDiagnostics(settings_)
				if err != nil {
					return err
				}
				fmt.Println("诊断包：", p)
			}
			for _, ch := range checks {
				if ch.Status == "bad" {
					return errors.New("存在失败项，见上")
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&bundle, "bundle", false, "打包诊断信息（提交 issue 时附上）")
	c.Flags().BoolVar(&fix, "fix", false, "一键修复可处理的异常（写 PATH 前自动快照）")
	c.Flags().BoolVar(&undo, "undo", false, "撤销上一次 --fix 对用户 PATH 的改动")
	return c
}

// —— autostart ——

func autostartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "autostart <on|off|status>",
		Short: "开机自启（HKCU Run，免管理员）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch strings.ToLower(args[0]) {
			case "on":
				exe := guiExePath()
				if exe == "" {
					return core.W(core.ErrNotFound, "同目录没找到 EnvSwitch.exe；GUI 版才可自启")
				}
				if err := sys.SetAutostart("EnvSwitch", `"`+exe+`" --tray`); err != nil {
					return err
				}
				settings_.Autostart = true
				settings_.Save()
				fmt.Println("开机自启已开启：", exe)
			case "off":
				if err := sys.RemoveAutostart("EnvSwitch"); err != nil {
					return err
				}
				settings_.Autostart = false
				settings_.Save()
				fmt.Println("开机自启已关闭")
			case "status":
				if v := sys.GetAutostart("EnvSwitch"); v != "" {
					fmt.Println("开启:", v)
				} else {
					fmt.Println("未开启")
				}
			default:
				return core.W(core.ErrUsage, "用法: envs autostart on|off|status")
			}
			return nil
		},
	}
}

func guiExePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for _, name := range []string{"EnvSwitch.exe", "envswitch.exe"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// —— config ——

func configCmd() *cobra.Command {
	c := &cobra.Command{Use: "config", Short: "查看与修改配置"}
	c.AddCommand(
		&cobra.Command{
			Use: "show", Short: "显示全部配置", Args: cobra.NoArgs,
			Run: func(cmd *cobra.Command, args []string) { outJSON(settings_) },
		},
		&cobra.Command{
			Use:   "set <key> <value>",
			Args:  cobra.ExactArgs(2),
			Short: "mirror.node / mirror.python / mirror.go / proxy / store",
			RunE: func(cmd *cobra.Command, args []string) error {
				k, v := args[0], args[1]
				switch {
				case strings.HasPrefix(k, "mirror."):
					settings_.Mirrors[strings.TrimPrefix(k, "mirror.")] = v
				case k == "proxy":
					settings_.Proxy = v
				case k == "store":
					settings_.StorePath = v
				default:
					return core.W(core.ErrUsage, "未知配置键 %q", k)
				}
				if err := settings_.Save(); err != nil {
					return err
				}
				fmt.Println(k, "=", v)
				return nil
			},
		},
	)
	return c
}

// —— path（注册表 PATH 维护）——

func pathCmd() *cobra.Command {
	c := &cobra.Command{Use: "path", Short: "用户 PATH 维护"}
	c.AddCommand(
		&cobra.Command{
			Use:   "restore",
			Short: "移除 EnvSwitch 写入的全部 PATH 条目",
			RunE: func(cmd *cobra.Command, args []string) error {
				changed, err := core.StripPATH(settings_)
				if err != nil {
					return err
				}
				if !changed {
					fmt.Println("PATH 中没有 EnvSwitch 条目")
					return nil
				}
				fmt.Println("已移除 EnvSwitch 条目并广播（旧值快照在 path-backup.json）")
				return nil
			},
		},
		&cobra.Command{
			Use:   "ensure",
			Short: "写入/校验 EnvSwitch 的 PATH 条目",
			RunE: func(cmd *cobra.Command, args []string) error {
				changed, err := core.EnsurePATH(settings_)
				if err != nil {
					return err
				}
				fmt.Println(map[bool]string{true: "已写入", false: "无需变更"}[changed])
				return nil
			},
		},
	)
	return c
}

// —— clean（下载缓存）——

func cleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "清理下载缓存（不动已安装版本）",
		RunE: func(cmd *cobra.Command, args []string) error {
			before := core.DirSize(settings.DownloadsDir(settings_))
			os.RemoveAll(settings.DownloadsDir(settings_))
			fmt.Printf("已释放 %s\n", core.HumanSize(before))
			return nil
		},
	}
}

// —— self（热更新）——

func selfCmd(ctx context.Context) *cobra.Command {
	c := &cobra.Command{Use: "self", Short: "EnvSwitch 自身的热更新"}
	c.AddCommand(
		&cobra.Command{
			Use: "update", Short: "检查并安装新版本（自替换）", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				cur := mainVersion()
				m, asset, err := core.CheckUpdate(cmd.Context(), settings_.Proxy, cur)
				if err != nil {
					return err
				}
				if m == nil {
					fmt.Printf("已是最新（%s）\n", cur)
					return nil
				}
				fmt.Printf("发现新版本 %s（%s）\n", m.Version, core.HumanSize(asset.Size))
				su := core.NewSelfUpdate(settings_)
				prog := func(done, total int64, speed float64) {
					fmt.Fprintf(os.Stderr, "\r下载 %2.0f%% (%s/s)", float64(done)/float64(total)*100, core.HumanSize(int64(speed)))
				}
				files, err := su.Download(cmd.Context(), asset, prog)
				if err != nil {
					fmt.Fprintln(os.Stderr)
					return err
				}
				exe, _ := os.Executable()
				if err := su.Apply(files, exe); err != nil {
					fmt.Fprintln(os.Stderr)
					return err
				}
				fmt.Fprintf(os.Stderr, "\n已更新 %s → %s\n", cur, m.Version)
				if strings.EqualFold(filepath.Base(exe), "EnvSwitch.exe") {
					r := exec.Command(exe, "--updated")
					r.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x8 | 0x200}
					_ = r.Start()
				}
				return nil
			},
		},
		&cobra.Command{
			Use: "rollback", Short: "回滚到上一版本（.old 保留期内）", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				exe, _ := os.Executable()
				if _, err := os.Stat(exe + ".old"); err != nil {
					return core.W(core.ErrNotFound, "没有可回滚的上一版本")
				}
				if err := core.NewSelfUpdate(settings_).DoRollback(exe); err != nil {
					return err
				}
				fmt.Println("已回滚到上一版本")
				return nil
			},
		},
	)
	return c
}

// —— gui（打开桌面应用）——

func guiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gui",
		Short: "打开 EnvSwitch 桌面应用",
		RunE: func(cmd *cobra.Command, args []string) error {
			exe := guiExePath()
			if exe == "" {
				return core.W(core.ErrNotFound, "同目录未找到 EnvSwitch.exe，请先安装桌面版")
			}
			c := exec.Command(exe)
			c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x8} // DETACHED_PROCESS
			return c.Start()
		},
	}
}

// ———————————————— 目录级版本绑定（方案 §6.1 / §6.3 / §7.1）———————————————

// bindDirFlag 解析 --cwd（空 = 进程当前目录）。
func bindDirFlag(cwd string) string {
	if strings.TrimSpace(cwd) != "" {
		return cwd
	}
	d, _ := os.Getwd()
	return d
}

// outJSONOnly 只往 stdout 打 JSON（§7.1：--json 时 stdout 必须是合法 JSON，表格不得混入）。
func outJSONOnly(v any) {
	if v == nil {
		fmt.Println("null")
		return
	}
	// 空集必须是 []，不能是 null（§7.1 契约）
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.Len() == 0 {
		fmt.Println("[]")
		return
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println("null")
		return
	}
	fmt.Println(string(data))
}

func bindWarnToStderr(warns []string) {
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "提示:", w)
	}
}

// —— envs bind ——

func bindCmd() *cobra.Command {
	var cwd, note, auto string
	var envPairs []string
	var rewrite bool

	c := &cobra.Command{
		Use:   "bind <tool> <ver> | bind enable | bind disable | bind autodetect <off|prompt|on>",
		Short: "把目录绑定到具体版本（默认只写表，不动全局 PATH / junction）",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, warns := core.LoadBindings()
			bindWarnToStderr(warns)
			switch strings.ToLower(args[0]) {
			case "enable":
				b.Enabled = true
				return saveBindingsCmd(b, rewrite)
			case "disable":
				b.Enabled = false
				return saveBindingsCmd(b, rewrite)
			case "autodetect":
				if len(args) < 2 {
					return core.W(core.ErrUsage, "用法: envs bind autodetect off|prompt|on")
				}
				auto = strings.ToLower(args[1])
				switch auto {
				case core.AutoDetectOff, core.AutoDetectPrompt, core.AutoDetectOn:
				default:
					return core.W(core.ErrUsage, "autoDetect 只能是 off / prompt / on（§4.1）")
				}
				b.AutoDetect = auto
				return saveBindingsCmd(b, rewrite)
			case "why":
				return nil // 由子命令处理（注册在 c.AddCommand）
			}
			if len(args) < 2 {
				return core.W(core.ErrUsage, "用法: envs bind <tool> <ver> [--cwd 目录]，例如 envs bind node 16")
			}
			tool, ver := args[0], args[1]
			env := map[string]string{}
			for _, p := range envPairs {
				k, v, ok := strings.Cut(p, "=")
				if !ok {
					return core.W(core.ErrUsage, "--env 要写成 K=V：%q", p)
				}
				env[strings.TrimSpace(k)] = v
			}
			dir := bindDirFlag(cwd)
			ent, err := core.SetBindingTool(b, dir, tool, ver, env, note, "cli")
			if err != nil {
				return err
			}
			if !b.Enabled {
				fmt.Fprintln(os.Stderr, "提示：目录绑定总开关还没开（§4.1 灰度默认关闭）。执行 envs bind enable 后钩子与 envs run 才会用上这条绑定。")
			}
			if n := core.BindAliasNote(tool); n != "" && !flagJSON {
				fmt.Fprintln(os.Stderr, "提示："+n)
			}
			if err := saveBindingsCmd(b, false); err != nil {
				return err
			}
			if flagJSON {
				outJSONOnly(map[string]any{"path": ent.Path, "tools": ent.Tools, "env": ent.Env,
					"enabled": b.Enabled, "genTable": core.GenTablePath()})
				return nil
			}
			fmt.Printf("已绑定 %s → %s %s\n", ent.Path, tool, ver)
			fmt.Println("新开终端生效（或用 envs run / envs shell）；已打开的终端不会变化。")
			return nil
		},
	}
	c.Flags().StringVar(&cwd, "cwd", "", "绑定哪个目录（默认当前目录）")
	c.Flags().StringArrayVar(&envPairs, "env", nil, "目录级环境变量覆盖 K=V，可重复（§4.4，只在会话/envs run 内注入）")
	c.Flags().StringVar(&note, "note", "", "备注")
	c.Flags().BoolVar(&rewrite, "rewrite", false, "enable/disable 时同步重生成 shim 与钩子（§11 回滚半径）")
	c.AddCommand(bindWhyCmd())
	return c
}

// saveBindingsCmd 写表 + 重生成扁平面，并把写侧拒写与锁降级如实报出来。
func saveBindingsCmd(b *core.Bindings, rewrite bool) error {
	warns, err := core.SaveBindings(b, tools.All(), settings_)
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "提示:", w)
	}
	if err != nil {
		if errors.Is(err, core.ErrBindingsReadOnly) {
			fmt.Fprintln(os.Stderr, "绑定表正被另一个进程写入，本次改动没有落地（§4.5 降级只读）。请稍后重试。")
			return core.W(core.ErrPerm, "%v", err)
		}
		if errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "access is denied") ||
			strings.Contains(err.Error(), "拒绝访问") {
			return core.W(core.ErrPerm, "无法保存绑定（目录不可写）。命令行不受影响：%v", err)
		}
		return err
	}
	if rewrite {
		// 层 2 shim 模板归 EnsureShims（每次 CLI 调用自愈），这里只保证扁平面与主表一致（已重生成）。
		fmt.Fprintln(os.Stderr, "提示：shim 模板由 EnsureShims 在下次任意 envs 调用时重写；本次已重生成 bindings.gen.txt。")
	}
	// 跨过层 2 的行数护栏时如实说一句（口径与 agent-A 的 shims.go/doctor.go 同一常量、同一措辞）。
	// 走 stderr：--json 时 stdout 必须只有 JSON（§7.1），且不新增退出码语义。
	if n := core.Layer2GuardNotice(core.GenTableRows()); n != "" {
		fmt.Fprintln(os.Stderr, "提示："+n)
	}
	return nil
}

func bindWhyCmd() *cobra.Command {
	var cwd string
	c := &cobra.Command{
		Use:   "why",
		Short: "解释这个目录的版本是怎么定下来的（解析链每一层 + 被跳过的原因）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, warns := core.LoadBindings()
			bindWarnToStderr(warns)
			r := core.ResolveBindings(bindDirFlag(cwd), b, settings_, tools.All())
			if flagJSON {
				outJSONOnly(r)
				return nil
			}
			fmt.Print(r.ExplainText())
			return nil
		},
	}
	c.Flags().StringVar(&cwd, "cwd", "", "要解释哪个目录（默认当前目录）")
	return c
}

// —— envs unbind ——

func unbindCmd() *cobra.Command {
	var cwd string
	var all bool
	c := &cobra.Command{
		Use:   "unbind [tool] | unbind --all",
		Short: "取消目录绑定（只删表里的条目；PATH / 注册表 / junction 一概不碰，§11 回滚半径）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, warns := core.LoadBindings()
			bindWarnToStderr(warns)
			dir := bindDirFlag(cwd)
			tool := ""
			if len(args) == 1 {
				tool = args[0]
			}
			if all {
				tool = ""
			}
			changed, left := core.DeleteBindingTool(b, dir, tool)
			if !changed {
				if flagJSON {
					outJSONOnly(map[string]any{"path": core.NormalizePath(dir), "changed": false, "toolsLeft": left})
					return nil
				}
				fmt.Println("该目录没有对应的绑定，未做改动。")
				return nil
			}
			if err := saveBindingsCmd(b, false); err != nil {
				return err
			}
			if flagJSON {
				outJSONOnly(map[string]any{"path": core.NormalizePath(dir), "changed": true, "toolsLeft": left})
				return nil
			}
			if left == 0 {
				fmt.Printf("已删除该目录的整条绑定：%s\n", core.NormalizePath(dir))
				return nil
			}
			fmt.Printf("已取消该目录的一个工具绑定，还剩 %d 个：%s\n", left, core.NormalizePath(dir))
			return nil
		},
	}
	c.Flags().StringVar(&cwd, "cwd", "", "目标目录（默认当前目录）")
	c.Flags().BoolVar(&all, "all", false, "删除该目录的整条绑定（含所有工具与 env 覆盖）")
	return c
}

// —— envs bindings ——

func bindingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bindings",
		Short: "列出全部目录绑定与实际解析到的版本",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, warns := core.LoadBindings()
			bindWarnToStderr(warns)
			rows := core.BindingRows(b, settings_, tools.All())
			if flagJSON {
				outJSONOnly(rows)
				return nil
			}
			fmt.Printf("绑定表：%s（enabled=%v autoDetect=%s maxDepth=%d，共 %d 条）\n",
				b.Path(), b.Enabled, b.AutoDetect, b.MaxDepth, b.Count())
			if len(rows) == 0 {
				fmt.Println("（空）用 envs bind node 16 --cwd <目录> 添加第一条。")
				return nil
			}
			w := tw()
			fmt.Fprintln(w, "状态\t目录\t工具=绑定值\t实际解析\t来源\t备注")
			for _, r := range rows {
				mark := map[string]string{"ok": "✓", "warn": "!", "bad": "✗"}[r.Status]
				keys := make([]string, 0, len(r.Tools))
				for k := range r.Tools {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for i, k := range keys {
					res := r.Resolved[k]
					if res == "" {
						res = "—"
					}
					src := ""
					if i == 0 {
						src = r.Source
					}
					fmt.Fprintf(w, "%s\t%s\t%s=%s\t%s\t%s\t%s\n", mark, col1(r.Path, i, len(keys)), k, r.Tools[k], res, dash(src), dash(r.Note))
				}
			}
			w.Flush()
			fmt.Println("新开的终端才会用上绑定；已打开的终端不变。")
			return nil
		},
	}
}

// col1 只在第一条子行显示目录名，表格更紧凑。
func col1(s string, i, n int) string {
	if n > 1 && i > 0 {
		return "  ↪"
	}
	return s
}

// —— envs resolve ——

func resolveCmd() *cobra.Command {
	var cwd, printMode string
	var explain bool
	c := &cobra.Command{
		Use:   "resolve [tool]",
		Short: "解析某目录该用什么版本（层 3 与钩子共用同一判定）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, warns := core.LoadBindings()
			bindWarnToStderr(warns)
			r := core.ResolveBindings(bindDirFlag(cwd), b, settings_, tools.All())
			tool := ""
			if len(args) == 1 {
				tool = strings.ToLower(args[0])
			}
			if printMode != "" {
				return printResolved(r, tool, printMode)
			}
			if flagJSON {
				outJSONOnly(r)
				return nil
			}
			fmt.Print(r.ExplainText())
			if explain {
				fmt.Println("说明：任何一层失败都只回退全局版本，绝不阻塞命令（§10）。")
			}
			return nil
		},
	}
	c.Flags().StringVar(&cwd, "cwd", "", "要解析的目录（默认当前目录）")
	c.Flags().StringVar(&printMode, "print", "", "只输出一行/几行：path|exe|dir|env")
	c.Flags().BoolVar(&explain, "explain", false, "附带口径说明")
	return c
}

func printResolved(r *core.BindingResolve, tool, mode string) error {
	pick := func() *core.BindingInjection {
		for i := range r.Inject {
			x := &r.Inject[i]
			if tool == "" || x.Tool == tool || core.BindHostOf(x.Tool) == tool {
				return x
			}
		}
		return nil
	}
	switch mode {
	case "path":
		fmt.Println(strings.Join(r.InjectDirs(), ";"))
		return nil
	case "env":
		for _, k := range r.EnvKeys() {
			fmt.Printf("%s=%s\n", k, r.Env[k])
		}
		return nil
	case "exe", "dir":
		x := pick()
		if x == nil {
			reason := "该目录没有生效的绑定"
			if len(r.Skips) > 0 {
				reason = r.Skips[0].Reason
			}
			return core.W(core.ErrNotFound, "%s（%s）", reason, r.Cwd)
		}
		if mode == "dir" {
			fmt.Println(x.BinDir)
			return nil
		}
		ad := tools.ByID(core.BindHostOf(x.Tool))
		if ad == nil {
			return core.W(core.ErrUsage, "未知工具 %q", x.Tool)
		}
		exeName := core.PathExeName(ad)
		if exeName == "" {
			return core.W(core.ErrUsage, "%s 没有 PATH 入口名，无法给出 exe 路径", x.Tool)
		}
		fmt.Println(filepath.Join(x.BinDir, exeName)) // §6.3：一行绝对路径，喂 JetBrains / launch.json
		return nil
	}
	return core.W(core.ErrUsage, "--print 只能取 path|exe|dir|env（§6.3）")
}

// —— envs run / envs shell（层 3：万能托底，§6.3）——

// explicitBindings 把 --tool id@ver 变成优先级最高的临时绑定条目。
func explicitBindings(specs []string, dir string) (*core.Binding, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	ent := &core.Binding{Path: dir, Tools: map[string]string{}, Source: "cli-run"}
	for _, s := range specs {
		tool, ver := parseToolVer([]string{s})
		if tool == "" || ver == "" {
			return nil, core.W(core.ErrUsage, "--tool 要写成 id@ver 或 id ver：%q", s)
		}
		if ok, _, why := core.BindGate(tool); !ok {
			return nil, core.W(core.ErrUsage, "%s", why)
		}
		ent.Tools[strings.ToLower(tool)] = ver
	}
	return ent, nil
}

func runCmd(ctx context.Context) *cobra.Command {
	var cwd string
	var toolSpecs []string
	var installMissing bool
	c := &cobra.Command{
		Use:   "run [--cwd D] [--tool id@ver]... -- <命令> [参数...]",
		Short: "按目录绑定的版本执行一条命令（结构缺口的唯一托底；退出码原样透传）",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return core.W(core.ErrUsage, "用法: envs run -- <命令>，例如 envs run -- npm run dev")
			}
			b, warns := core.LoadBindings()
			bindWarnToStderr(warns)
			dir := bindDirFlag(cwd)
			ent, err := explicitBindings(toolSpecs, dir)
			if err != nil {
				return err
			}
			if ent != nil {
				b.Bindings = append([]core.Binding{*ent}, b.Bindings...)
				b.Enabled = true
			}
			r := core.ResolveBindings(dir, b, settings_, tools.All())
			// §10 第 1 行的唯一例外开关：只有显式传 --install-if-missing 才走安装链路，
			// 默认关（core.MaybeInstallMissing 在 flag=false 时一个字节都不发出去）。
			if installMissing {
				core.InstallMissingHook = func(ctx context.Context, tool, expr string) error {
					ad := tools.ByID(tool)
					if ad == nil {
						return core.W(core.ErrUsage, "未知工具 %q", tool)
					}
					target, stale, err := core.ResolveRemote(ctx, settings_, ad, expr)
					if err != nil {
						return err
					}
					if stale {
						fmt.Fprintln(os.Stderr, "提示：远程索引来自缓存")
					}
					prog := func(done, total int64, speed float64) {
						if flagJSON {
							return
						}
						fmt.Fprintf(os.Stderr, "\r下载 %s%s", target.Archive, core.HumanSize(done))
					}
					return core.InstallPipeline(ctx, settings_, ad, *target, prog)
				}
				tried, err := core.MaybeInstallMissing(ctx, r, true)
				if err != nil {
					fmt.Fprintln(os.Stderr)
					return err
				}
				if len(tried) > 0 {
					fmt.Fprintf(os.Stderr, "\r已补装 %d 项，重新解析该目录\n", len(tried))
					r = core.ResolveBindings(dir, b, settings_, tools.All())
				}
			}
			if flagJSON {
				// 机器可读模式下先把解析结果发出去（stderr 归诊断），再执行
				outJSONOnly(r)
			}
			env := r.ApplyEnv(os.Environ())
			code, err := spawnWithEnv(env, r.InjectDirs(), dir, args)
			if err != nil {
				return err
			}
			if code != 0 {
				os.Exit(code) // §7.1 不新增退出码语义：透传子进程真实码
			}
			return nil
		},
	}
	c.Flags().StringVar(&cwd, "cwd", "", "以哪个目录为绑定基准（同时作为子进程工作目录）")
	c.Flags().StringArrayVar(&toolSpecs, "tool", nil, "临时指定 id@ver，可重复；优先级最高（§6.3）")
	c.Flags().BoolVar(&installMissing, "install-if-missing", false, "绑定的版本未装时先装再跑（§10 第 1 行的唯一例外；默认关，不会自动下载）")
	c.Flags().SetInterspersed(false)
	return c
}

func shellCmd() *cobra.Command {
	var cwd string
	var ps, cmdShell, bash bool
	c := &cobra.Command{
		Use:   "shell [--cwd D] [--ps|--cmd|--bash]",
		Short: "开一个已注入目录绑定版本的交互终端",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, warns := core.LoadBindings()
			bindWarnToStderr(warns)
			dir := bindDirFlag(cwd)
			r := core.ResolveBindings(dir, b, settings_, tools.All())
			env := r.ApplyEnv(os.Environ())
			var argv []string
			switch {
			case cmdShell:
				argv = []string{"cmd.exe"}
			case bash:
				argv = []string{"bash", "--login", "-i"}
			case ps:
				argv = []string{"powershell.exe", "-NoExit"}
			default:
				argv = []string{"powershell.exe", "-NoExit"}
			}
			if !flagJSON {
				if len(r.InjectDirs()) == 0 {
					fmt.Println("该目录没有生效的绑定，开出的终端就是全局版本。")
				} else {
					fmt.Printf("已注入：%s\n", strings.Join(r.InjectDirs(), " ; "))
					for _, n := range r.Notes {
						fmt.Println("·", n)
					}
				}
			} else {
				outJSONOnly(r)
			}
			code, err := spawnWithEnv(env, r.InjectDirs(), dir, argv)
			if err != nil {
				return err
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
	c.Flags().StringVar(&cwd, "cwd", "", "以哪个目录为绑定基准")
	c.Flags().BoolVar(&ps, "ps", false, "PowerShell（默认）")
	c.Flags().BoolVar(&cmdShell, "cmd", false, "cmd.exe")
	c.Flags().BoolVar(&bash, "bash", false, "bash（需在 PATH 上，Git Bash）")
	return c
}

// spawnWithEnv 用给定环境块与目录启动子进程，stdio 全透传（§13-8 的 TTY 行为原样交给系统）。
func spawnWithEnv(env []string, dirs []string, dir string, argv []string) (int, error) {
	// R6：必须在**注入后的环境块 PATH** 上找命令。exec.LookPath 走的是 envs.exe 自己的
	// PATH，在系统 PATH 抢位的机器上会命中全局那个（§2-L1/L6），cmd.Env 里的新 PATH
	// 对已解析出的绝对路径不起作用 —— 绑定就变成假生效。
	exe, ok := core.LookupInEnv(env, argv[0])
	if !ok {
		hint := "（本次没有注入目录）"
		if len(dirs) > 0 {
			hint = "；已前置：" + strings.Join(dirs, " ; ")
		}
		return 0, core.W(core.ErrNotFound, "在注入后的 PATH 上找不到命令 %q%s", argv[0], hint)
	}
	c := exec.Command(exe, argv[1:]...)
	c.Args[0] = argv[0] // argv0 保持用户输入的裸名，工具自身行为不变
	c.Env = env
	if d := core.NormalizePath(dir); d != "" {
		if fi, e := os.Stat(d); e == nil && fi.IsDir() {
			c.Dir = d
		}
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

// —— envs init（层 1 会话注入，§6.1）——

func initCmd() *cobra.Command {
	var appendFlag, autorun, removeSet bool
	var remove string
	c := &cobra.Command{
		Use:   "init <powershell|bash|cmd>",
		Short: "安装/预览/卸载 shell 钩子（未经你的 --append/--autorun 绝不改任何启动文件，§12）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if removeSet {
				target := remove
				if len(args) == 1 {
					target = args[0]
				}
				res, err := core.RemoveInit(target)
				if flagJSON {
					outJSONOnly(res)
				}
				if err != nil {
					return err
				}
				fmt.Printf("已移除 %d 处钩子写入：%s\n", res.Changed, strings.Join(res.Targets, " | "))
				for _, s := range res.Skipped {
					fmt.Fprintln(os.Stderr, "跳过:", s)
				}
				return nil
			}
			if len(args) != 1 {
				return core.W(core.ErrUsage, "用法: envs init powershell|bash|cmd [--append|--autorun]，卸载用 envs init --remove *")
			}
			shell := args[0]
			doWrite := appendFlag || autorun
			res, err := core.InstallInit(shell, core.InitExePath(), doWrite, autorun)
			if err != nil {
				if flagJSON {
					outJSONOnly(res)
				}
				return err
			}
			if flagJSON {
				if !doWrite {
					outJSONOnly(res)
					return nil
				}
				res.Block = "" // 已写入就别把整块文本再塞进 JSON
				outJSONOnly(res)
				return nil
			}
			if !doWrite {
				fmt.Printf("将要写入 %s 的钩子块（未落盘；加 --append 或 --autorun 才真写）：\n\n", shell)
				fmt.Print(res.Block)
				fmt.Println("\n" + res.Note)
				return nil
			}
			fmt.Printf("已写入 %d 处：\n", res.Changed)
			for _, t := range res.Targets {
				fmt.Println(" ·", t)
			}
			for _, s := range res.Skipped {
				fmt.Fprintln(os.Stderr, "跳过:", s)
			}
			fmt.Println(res.Note)
			fmt.Println("卸载：envs init --remove " + shell + "（只删自己写的块，BEGIN/END 标记定位）")
			return nil
		},
	}
	c.Flags().BoolVar(&appendFlag, "append", false, "真的追加/更新到 profile 或 ~/.bashrc")
	c.Flags().BoolVar(&autorun, "autorun", false, "cmd 专用：写 home\\bin\\envs-autorun.cmd 与 HKCU AutoRun")
	c.Flags().BoolVar(&removeSet, "remove", false, "移除本工具写入的钩子块（配合 shell 名或 *）")
	c.Flags().StringVar(&remove, "remove-shell", "", "--remove 的目标 shell（也可把 shell 名作为位置参数）")
	_ = c.Flags().MarkHidden("remove-shell")
	return c
}

// —— __bind-resolve（隐藏命令：cmd AutoRun / shim 的解析出口）——

func bindResolveCmd() *cobra.Command {
	var cwd, shell string
	c := &cobra.Command{
		Use:    core.BindResolveHiddenCmd,
		Short:  "（内部）按扁平表输出 cmd 可直接执行的 set 行",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !strings.EqualFold(shell, "cmd") {
				return core.W(core.ErrUsage, "--shell 目前只支持 cmd（PowerShell / bash 钩子走扁平表，零进程创建）")
			}
			// 主路径不依赖 bindings.json：先查预生成的扁平表（§7.1 / §2-L10）
			dir := bindDirFlag(cwd)
			inj, env, terr := core.GenTableLookupErr(dir)
			if terr != nil {
				// §10 第 7 行：超时 / 读不动 → 什么都不输出，本次自然用全局版本，命令不被阻塞
				fmt.Fprintln(os.Stderr, "EnvSwitch 目录解析未响应（可能被安全软件拦截），本次已回退全局版本:", terr)
				return nil
			}
			r := &core.BindingResolve{Cwd: dir, CmpKey: core.CmpKey(dir), Enabled: true, Inject: inj, Env: env}
			lines, skipped := core.BindResolveSetLines(r)
			if skipped != "" {
				fmt.Fprintln(os.Stderr, "提示:", skipped)
			}
			if len(lines) == 0 {
				fmt.Fprintln(os.Stderr, "EnvSwitch: helper not found, using global version.")
				return nil
			}
			for _, ln := range lines {
				fmt.Println(ln)
			}
			return nil
		},
	}
	c.Flags().StringVar(&cwd, "cwd", "", "按该目录解析（默认当前目录）")
	c.Flags().StringVar(&shell, "shell", "cmd", "输出格式")
	return c
}
