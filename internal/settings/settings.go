// Package settings 负责 EnvSwitch 的目录布局与用户配置。
// 一切可写数据都在 home（默认 %LOCALAPPDATA%\EnvSwitch，可用 ENVS_HOME 覆盖），
// 不触碰系统盘之外的路径，保证免管理员运行。
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Settings struct {
	Version           int               `json:"version"`
	Autostart         bool              `json:"autostart"`
	CloseToTray       bool              `json:"closeToTray"`
	CheckUpdateOnStart bool             `json:"checkUpdateOnStart"`
	StorePath         string            `json:"storePath,omitempty"` // 空则用默认 home\store
	Mirrors           map[string]string `json:"mirrors"`
	Proxy             string            `json:"proxy"` // GitHub 代理前缀，如 https://ghproxy.net/
	IndexCacheMinutes int               `json:"indexCacheMinutes"`
	EnabledTools      []string          `json:"enabledTools"`

	path string // 磁盘位置，Save 用
}

var DefaultMirrors = map[string]string{
	"node":   "npmmirror",
	"npm":    "npmmirror",
	"yarn":   "npmmirror",
	"pnpm":   "npmmirror",
	"python": "auto", // GitHub API 快探失败自动退 NJU 镜像
	"go":     "google.cn",
}

var DefaultTools = []string{"node", "npm", "pnpm", "yarn", "python", "go"}

// Home 返回数据根目录（不含 store 覆盖逻辑）。
func Home() string {
	if v := os.Getenv("ENVS_HOME"); v != "" {
		return v
	}
	base, err := os.UserConfigDir() // %APPDATA% 在 Windows；改用 LocalAppData 更稳
	if err != nil || base == "" {
		base = filepath.Join(os.TempDir(), "EnvSwitch")
	}
	if runtime.GOOS == "windows" {
		if ld := os.Getenv("LOCALAPPDATA"); ld != "" {
			base = ld
		}
	}
	return filepath.Join(base, "EnvSwitch")
}

func Load() (*Settings, error) {
	s := &Settings{
		Version:            1,
		Autostart:          false,
		CloseToTray:        true,
		CheckUpdateOnStart: true,
		Mirrors:            map[string]string{},
		IndexCacheMinutes:  10,
		EnabledTools:       append([]string{}, DefaultTools...),
	}
	for k, v := range DefaultMirrors {
		s.Mirrors[k] = v
	}
	home := Home()
	s.path = filepath.Join(home, "settings.json")
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	for k, v := range DefaultMirrors {
		if _, ok := s.Mirrors[k]; !ok {
			s.Mirrors[k] = v
		}
	}
	if len(s.EnabledTools) == 0 {
		s.EnabledTools = append([]string{}, DefaultTools...)
	}
	return s, nil
}

func (s *Settings) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// —— 目录布局 ——

func HomeDir() string { return Home() }

func StoreDir(s *Settings) string {
	if s.StorePath != "" {
		return s.StorePath
	}
	return filepath.Join(Home(), "store")
}
func CurrentDir(s *Settings) string  { return filepath.Join(Home(), "current") }
func DownloadsDir(s *Settings) string { return filepath.Join(Home(), "downloads") }
func CacheDir(s *Settings) string     { return filepath.Join(Home(), "downloads", "cache") }
func LogsDir(s *Settings) string      { return filepath.Join(Home(), "logs") }
func UpdateDir(s *Settings) string    { return filepath.Join(Home(), "update") }
func PathBackup(s *Settings) string   { return filepath.Join(Home(), "path-backup.json") }

// CurrentLink 返回工具激活 junction 路径，如 home\current\node
func CurrentLink(s *Settings, tool string) string {
	return filepath.Join(CurrentDir(s), tool)
}

// ToolStoreDir 返回工具版本库父目录 home\store\<tool>
func ToolStoreDir(s *Settings, tool string) string {
	return filepath.Join(StoreDir(s), tool)
}

// VersionDir 返回 home\store\<tool>\<ver>
func VersionDir(s *Settings, tool, ver string) string {
	return filepath.Join(ToolStoreDir(s, tool), ver)
}

// OwnedPathEntries 返回需要写入用户 PATH 的五条路径（按优先级顺序）：
// 三个 runtime 激活链接 + js 工具 shims + envs.exe 所在目录（安装目录）。
// npm/pnpm/yarn 一律经 shims，不直接暴露包内 .cmd（其路径假设 node_modules
// 布局，解包形态会断链）。
func OwnedPathEntries(s *Settings) []string {
	return []string{
		CurrentLink(s, "node"),
		CurrentLink(s, "python"),
		CurrentLink(s, "go"),
		ShimsDir(s),
		ExeDir(),
	}
}

// ExeDir 返回当前可执行文件所在目录；go run 场景退化为 home。
func ExeDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return Home()
}

// ShimsDir 返回 shims 目录 home\bin\shims
func ShimsDir(s *Settings) string {
	return filepath.Join(Home(), "bin", "shims")
}

// IsOwnedPath 判断某条 PATH 是否归 EnvSwitch 所有：home 前缀（current\ 或 bin\shims）
// 或当前 exe 目录（安装目录，随 path restore 一并清理）。
func IsOwnedPath(entry string) bool {
	e := strings.ToLower(filepath.Clean(entry))
	if strings.EqualFold(e, filepath.Clean(ExeDir())) {
		return true
	}
	h := strings.ToLower(filepath.Clean(Home()))
	if !strings.HasPrefix(e, h+string(filepath.Separator)) {
		return false
	}
	rel := e[len(h):]
	return strings.Contains(rel, "\\current\\") || rel == "\\bin\\shims" || strings.HasPrefix(rel, "\\bin\\shims\\")
}
