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
	"runtime"
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
			_ = core.EnsureShims(s, tools.All()) // shim 模板升级自愈
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
	)
	if err := root.Execute(); err != nil {
		var pe *exec.Error
		if errors.As(err, &pe) {
			err = core.W(core.ErrUsage, "%v", err)
		}
		if code := exitCodeOf(err); code != 0 {
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
	var noPath bool
	c := &cobra.Command{
		Use:   "use <tool> <ver|latest|前缀>",
		Short: "切换激活版本（只重指激活链接，毫秒级）",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool, ver := parseToolVer(args)
			if ver == "" {
				ver = "latest"
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
	return &cobra.Command{
		Use:   "which <tool>",
		Short: "解析当前实际生效的可执行文件路径",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ad := mustTool(args[0])
			exe := core.PathExeName(ad)
			got := core.WhichResolved(exe)
			if got == "" {
				fmt.Printf("%s：PATH 上没有 %s\n", args[0], exe)
				return nil
			}
			fmt.Println(got)
			if !settings.IsOwnedPath(filepath.Dir(got)) {
				fmt.Println("（来源不是 EnvSwitch —— 用 envs doctor 查看 PATH 优先级）")
			}
			return nil
		},
	}
}

// —— doctor ——

func doctorCmd() *cobra.Command {
	var bundle bool
	c := &cobra.Command{
		Use:   "doctor",
		Short: "体检：PATH 优先级、激活链接、写权限、共存、WebView2",
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := core.Doctor(tools.All(), settings_)
			w := tw()
			for _, ch := range checks {
				mark := map[string]string{"ok": "✓", "warn": "!", "bad": "✗", "info": "·"}[ch.Status]
				fmt.Fprintf(w, "%s\t%s\t%s\n", mark, ch.Name, ch.Detail)
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
