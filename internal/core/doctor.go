package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HUIdada1/envswitch/internal/settings"
	"github.com/HUIdada1/envswitch/internal/sys"
)

// Check 单项诊断结果。
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok / warn / bad / info
	Detail string `json:"detail"`
}

// Doctor 体检：写权限、junction 健康、PATH 仲裁、共存管理器、WebView2、自启。
func Doctor(ads []Adapter, s *settings.Settings) []Check {
	var out []Check

	// 1 注册表写权限
	if sys.UserPathCanWrite() {
		out = append(out, Check{"注册表写入", "ok", `HKCU\Environment 可写`})
	} else {
		out = append(out, Check{"注册表写入", "bad", `HKCU\Environment 不可写，请检查组策略；可手动添加 PATH 条目`})
	}

	// 2 激活链接健康
	links := settings.OwnedPathEntries(s)
	alive, dead := 0, 0
	var deadNames []string
	for _, l := range links {
		if _, ok := junctionTarget(l); ok {
			alive++
			continue
		}
		tool := filepath.Base(l)
		if !anyInstallFor(ads, s, tool) { // 该工具一个版本都没有：不算死
			continue
		}
		dead++
		deadNames = append(deadNames, tool)
	}
	if dead == 0 {
		out = append(out, Check{"激活链接", "ok", fmt.Sprintf("%d 条 junction 有效", alive)})
	} else {
		out = append(out, Check{"激活链接", "bad", "失效: " + strings.Join(deadNames, ", ") + "（重新 use 即可修复）"})
	}

	// 3 PATH 注册与优先级
	raw, _ := sys.ReadUserPath()
	joined := strings.ToLower(os.ExpandEnv(sys.MachinePath() + ";" + raw))
	ownNode := strings.ToLower(filepath.Join(settings.Home(), "current", "node"))
	if i := strings.Index(joined, ownNode); i >= 0 {
		worst := -1
		for _, other := range []string{"nvm4w", "appdata\\roaming\\nvm", "scoop\\shims", "program files\\nodejs"} {
			if j := strings.Index(joined, other); j >= 0 && j < i {
				worst = j
			}
		}
		if worst >= 0 {
			out = append(out, Check{"PATH 优先级", "warn", "存在排在 EnvSwitch 之前的 node 来源；use 时会自动前移，或手动调整"})
		} else {
			out = append(out, Check{"PATH 优先级", "ok", "EnvSwitch 条目在生效位置"})
		}
	} else {
		out = append(out, Check{"PATH 注册", "info", "尚未启用：首次 use 会自动写入 5 条路径"})
	}

	// 4 共存管理器
	if root := NvmRoot(); root != "" {
		out = append(out, Check{"nvm-windows 共存", "info", "发现 " + root + "，其版本列入「收编」只读展示"})
	}
	if up := os.Getenv("USERPROFILE"); up != "" {
		if _, err := os.Stat(filepath.Join(up, "scoop")); err == nil {
			out = append(out, Check{"scoop 共存", "info", "检测到 scoop 目录；其 shims 参与 PATH 优先级竞争"})
		}
	}

	// 5 磁盘占用
	out = append(out, Check{"版本库占用", "info", HumanSize(DirSize(settings.StoreDir(s)))})

	// 6 WebView2（GUI 依赖）
	if sys.HasWebView2() {
		out = append(out, Check{"WebView2", "ok", "GUI 可用"})
	} else {
		out = append(out, Check{"WebView2", "warn", "未安装运行库，GUI 无法启动；CLI 不受影响"})
	}

	// 7 自启
	if cmd := sys.GetAutostart("EnvSwitch"); cmd != "" {
		out = append(out, Check{"开机自启", "ok", cmd})
	} else {
		out = append(out, Check{"开机自启", "info", "未开启"})
	}
	return out
}

func anyInstallFor(ads []Adapter, s *settings.Settings, tool string) bool {
	for _, ad := range ads {
		if ad.ID() == tool && len(scanStore(ad, s)) > 0 {
			return true
		}
	}
	return false
}

// BundleDiagnostics 把配置、PATH 现状、激活链接与最近日志打成一个 zip，
// 用户提 issue 时直接附件，不需要手抄任何系统信息。
func BundleDiagnostics(s *settings.Settings) (string, error) {
	dir := settings.LogsDir(s)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, fmt.Sprintf("diag-%s.json", time.Now().Format("20060102-150405")))
	bundle := map[string]any{
		"version":  BuildVersion(),
		"home":     settings.Home(),
		"store":    settings.StoreDir(s),
		"userPath": rawUserPath(),
		"owned":    settings.OwnedPathEntries(s),
		"settings": s,
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return "", err
	}
	return out, nil
}

func rawUserPath() string { v, _ := sys.ReadUserPath(); return v }
