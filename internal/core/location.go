package core

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MUHUIX/envswitch/internal/settings"
	"github.com/MUHUIX/envswitch/internal/sys"
)

// 本文件只做只读勘察：某工具装在哪个目录、激活链接指向哪、PATH/环境变量现状如何。
// 不写注册表、不建目录 —— 展示用的快照。

// PathEntry 一条与本工具相关的 PATH 注册项。
type PathEntry struct {
	Path    string `json:"path"`
	Role    string `json:"role"`    // 激活链接 / 命令 shim
	Present bool   `json:"present"` // 是否已在用户 PATH（HKCU\Environment）
	Exists  bool   `json:"exists"`  // 目录是否真实存在
	Note    string `json:"note"`
}

// EnvVar 与工具相关的环境变量现状。
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Set   bool   `json:"set"`
	Note  string `json:"note"`

	// 托管相关：ManageKey 非空表示这个变量可以在当前工具页开启托管；
	// Managed 表示已托管；ManageTool 是托管目标工具（共享变量会在别的工具页显示）。
	Managed    bool   `json:"managed"`
	ManageKey  string `json:"manageKey,omitempty"`
	ManageTool string `json:"manageTool,omitempty"`
	Target     string `json:"target,omitempty"`
}

// Shim 一个 JS 命令 shim 文件。
type Shim struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// ToolLocation 一个工具"装在哪、怎么进环境"的完整快照。
type ToolLocation struct {
	Home          string      `json:"home"`
	StoreDir      string      `json:"storeDir"`
	StoreDefault  string      `json:"storeDefault"`
	StoreCustom   bool        `json:"storeCustom"`
	ToolStoreDir  string      `json:"toolStoreDir"`
	VersionHint   string      `json:"versionHint"` // 新装一个版本会落到的目录（已有版本时给真路径）
	BinSub        string      `json:"binSub"`
	CurrentLink   string      `json:"currentLink"`
	CurrentTarget string      `json:"currentTarget"`
	CurrentOK     bool        `json:"currentOk"`
	PathEntries   []PathEntry `json:"pathEntries"`
	EnvVars       []EnvVar    `json:"envVars"`
	ShimsDir      string      `json:"shimsDir"`
	Shims         []Shim      `json:"shims"`
	ExeDir        string      `json:"exeDir"`
}

var envVarSpecs = map[string][]EnvVar{
	"node":   {{Key: "NODE_PATH", Note: "全局模块搜索路径（可选）"}},
	"python": {{Key: "PYTHONHOME", Note: "Python 安装根（可选）"}, {Key: "PYTHONPATH", Note: "模块搜索路径（可选）"}},
	"go":     {{Key: "GOROOT", Note: "Go 安装根（可选，go 命令能自解析）"}, {Key: "GOPATH", Note: "工作区目录"}, {Key: "GOMODCACHE", Note: "模块缓存目录"}},
	"rust":   {{Key: "CARGO_HOME", Note: "Cargo 数据目录"}, {Key: "RUSTUP_HOME", Note: "rustup 工具链目录"}},
	"php":    {{Key: "PHPRC", Note: "php.ini 所在目录（可选）"}},
	"java":   {{Key: "JAVA_HOME", Note: "JDK 根目录（Maven/Gradle 依赖它）"}},
	"maven":  {{Key: "JAVA_HOME", Note: "JDK 根目录（必需）"}, {Key: "MAVEN_HOME", Note: "Maven 根目录"}, {Key: "M2_HOME", Note: "Maven 2 兼容变量"}},
	"gradle": {{Key: "JAVA_HOME", Note: "JDK 根目录（必需）"}, {Key: "GRADLE_HOME", Note: "Gradle 根目录"}},
	"dotnet": {{Key: "DOTNET_ROOT", Note: ".NET 运行时根目录"}},
}

// Locate 汇总工具的安装位置与环境配置现状（只读）。
func Locate(ad Adapter, s *settings.Settings) ToolLocation {
	link := settings.CurrentLink(s, ad.ID())
	target, ok := junctionTarget(link)

	loc := ToolLocation{
		Home:          settings.Home(),
		StoreDir:      settings.StoreDir(s),
		StoreDefault:  filepath.Join(settings.Home(), "store"),
		StoreCustom:   s.StorePath != "",
		ToolStoreDir:  settings.ToolStoreDir(s, ad.ID()),
		BinSub:        ad.BinSub(),
		CurrentLink:   link,
		CurrentTarget: target,
		CurrentOK:     ok,
		ShimsDir:      settings.ShimsDir(s),
		ExeDir:        settings.ExeDir(),
	}
	if s.StorePath != "" && strings.EqualFold(filepath.Clean(s.StorePath), filepath.Clean(loc.StoreDefault)) {
		loc.StoreCustom = false
	}
	loc.VersionHint = filepath.Join(settings.ToolStoreDir(s, ad.ID()), "<版本号>")

	inPath := userPathSet()
	if jr, isJS := ad.(JSRunner); isJS {
		shims := jr.Shims()
		names := make([]string, 0, len(shims))
		for name := range shims {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			p := filepath.Join(settings.ShimsDir(s), name+".cmd")
			_, err := os.Stat(p)
			loc.Shims = append(loc.Shims, Shim{Name: name + ".cmd", Path: p, Exists: err == nil})
		}
		loc.PathEntries = append(loc.PathEntries, PathEntry{
			Path:    settings.ShimsDir(s),
			Role:    "命令 shim",
			Present: inPath[normPath(settings.ShimsDir(s))],
			Exists:  dirExists(settings.ShimsDir(s)),
			Note:    "shim 永远指向 current\\" + ad.ID() + "，切换版本不用改 PATH",
		})
	} else {
		owned := ownedRuntimeLink(ad.ID())
		if owned {
			loc.PathEntries = append(loc.PathEntries, PathEntry{
				Path:    link,
				Role:    "激活链接",
				Present: inPath[normPath(link)],
				Exists:  ok,
				Note:    "首次切换会自动写入用户 PATH",
			})
		} else {
			loc.PathEntries = append(loc.PathEntries, PathEntry{
				Path:    link,
				Role:    "激活链接",
				Present: inPath[normPath(link)],
				Exists:  ok,
				Note:    "不在 EnvSwitch 自动维护的 PATH 条目内；要在命令行直接使用需手动加入",
			})
		}
	}

	for _, spec := range envVarSpecs[ad.ID()] {
		v, set := os.LookupEnv(spec.Key)
		ev := EnvVar{Key: spec.Key, Value: v, Set: set, Note: spec.Note}
		if ms, ok := ManagedEnvSpecByKey(spec.Key); ok {
			ev.Managed = IsManagedEnv(s, spec.Key)
			ev.ManageTool = ms.Tool
			ownTool := ms.Tool == ad.ID()
			if ownTool {
				ev.ManageKey = spec.Key
				if root, alive := managedEnvRoot([]Adapter{ad}, s, ms.Tool); alive {
					ev.Target = root
				} else {
					ev.Target = settings.CurrentLink(s, ms.Tool)
				}
			}
			switch {
			case ev.Managed && ownTool:
				// 托管中：优先展示真正会写入的版本根；链接失效时退回注册表实际值
				if root, alive := managedEnvRoot([]Adapter{ad}, s, ms.Tool); alive {
					ev.Target, ev.Value, ev.Set = root, root, true
				} else if reg, had, err := readUserEnv(spec.Key); err == nil && had {
					ev.Value, ev.Set = reg, true
				} else {
					ev.Value, ev.Set = "", false
				}
				ev.Note = spec.Note + " · 由 EnvSwitch 托管"
			case ev.Managed:
				// 共享变量（如 maven 页的 JAVA_HOME）：不显示别人的托管目标，
				// 直接读注册表，避免渲染成空值
				if reg, had, err := readUserEnv(spec.Key); err == nil && had {
					ev.Value, ev.Set = reg, true
				}
				ev.Note = spec.Note + " · 由 EnvSwitch 托管（在 " + ms.Tool + " 页管理）"
			case ownTool:
				if reg, had, err := readUserEnv(spec.Key); err == nil && had &&
					!strings.EqualFold(strings.TrimSpace(reg), strings.TrimSpace(v)) {
					// 用户级与当前进程不一致：新开的终端会用用户级的值
					ev.Value, ev.Set = reg, true
					ev.Note = spec.Note + " · " + envVarOrigin(reg, s) + "（用户变量）"
				}
			}
		}
		if ev.Set && ev.Value != "" && ev.Note == spec.Note {
			ev.Note = spec.Note + " · " + envVarOrigin(ev.Value, s)
		}
		loc.EnvVars = append(loc.EnvVars, ev)
	}
	return loc
}

// ownedRuntimeLink 判断该工具是否属于 EnvSwitch 默认注册到 PATH 的三个运行时。
// 与 settings.OwnedPathEntries 保持一致 —— 那边改了这里要跟着改。
func ownedRuntimeLink(tool string) bool {
	switch tool {
	case "node", "python", "go":
		return true
	}
	return false
}

func normPath(p string) string {
	p = strings.TrimSpace(os.ExpandEnv(p))
	if p == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(p))
}

func userPathSet() map[string]bool {
	raw, _ := sys.ReadUserPath()
	out := map[string]bool{}
	for _, p := range strings.Split(raw, ";") {
		if k := normPath(p); k != "" {
			out[k] = true
		}
	}
	return out
}

// envVarOrigin 说明变量值来自 EnvSwitch 的激活目录还是外部安装。
func envVarOrigin(v string, s *settings.Settings) string {
	clean := normPath(v)
	if clean == "" {
		return "值无效"
	}
	if strings.HasPrefix(clean, normPath(settings.CurrentDir(s))+`\`) {
		return "指向 EnvSwitch 当前激活版本"
	}
	if strings.HasPrefix(clean, normPath(settings.StoreDir(s))+`\`) {
		return "指向 EnvSwitch 版本库"
	}
	if fi, err := os.Stat(os.ExpandEnv(strings.Trim(v, "\""))); err != nil || !fi.IsDir() {
		return "目录不存在"
	}
	return "外部安装目录"
}
