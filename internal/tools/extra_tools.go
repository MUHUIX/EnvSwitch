package tools

import (
	"context"
	"regexp"
	"sort"

	"github.com/MUHUIX/envswitch/internal/core"
	"github.com/MUHUIX/envswitch/internal/settings"
)

// Pip —— Python 官方包管理器。pip 以 wheel 形式随解释器分发，单独解包得不到
// pip.exe，因此只做本机版本检测（随 Python 一起切换），不提供独立安装入口。
type Pip struct{}

func (Pip) ID() string                { return "pip" }
func (Pip) Title() string             { return "pip" }
func (Pip) BinSub() string            { return "" }
func (Pip) Smoke() (string, []string) { return "pip.exe", []string{"--version"} }
func (Pip) ProbeFiles() []string      { return []string{"pip.exe"} }

func (Pip) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return nil, core.W(core.ErrUsage, "pip 随 Python 发行版提供，不单独安装；请切换 Python 版本，pip 会同步")
}

// Nvm —— nvm-windows Node 版本管理器本身
type Nvm struct{}

func (Nvm) ID() string           { return "nvm" }
func (Nvm) Title() string        { return "nvm-windows" }
func (Nvm) BinSub() string       { return "" }
func (Nvm) Smoke() (string, []string) { return "nvm.exe", []string{"version"} }
func (Nvm) ProbeFiles() []string { return []string{"nvm.exe"} }

var nvmAssetRe = regexp.MustCompile(`^nvm-noinstall\.zip$`)

func (Nvm) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	// 新版仓库前 20 条全是 2.x beta（会被 prerelease 过滤掉），
	// 稳定版 1.2.2 排在更后面，因此一次取 100 条。
	return ghBinaryReleases(context.Background(),
		"https://api.github.com/repos/coreybutler/nvm-windows/releases?per_page=100",
		nvmAssetRe, "nvm")
}

// Git —— Git for Windows（MinGit 便携版：解包即得 cmd/git.exe，可独立入 store）
type Git struct{}

func (Git) ID() string                { return "git" }
func (Git) Title() string             { return "Git" }
func (Git) BinSub() string            { return "cmd" }
func (Git) Smoke() (string, []string) { return "git.exe", []string{"--version"} }
func (Git) ProbeFiles() []string      { return []string{"cmd/git.exe", "bin/git.exe"} }

// 只取 MinGit 标准包：PortableGit 是 7z 自解压 exe，按单文件落位后拿不到 cmd/git.exe；
// busybox 变体条目不参与（版本相同会与标准包重复）。
var gitAssetRe = regexp.MustCompile(`(?i)^MinGit-\d+(?:\.\d+)*-64-bit\.zip$`)

func (Git) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return ghBinaryReleases(context.Background(),
		"https://api.github.com/repos/git-for-windows/git/releases?per_page=25",
		gitAssetRe, "git")
}

// Docker —— Docker CLI（官方静态 Windows 包）
type Docker struct{}

func (Docker) ID() string                { return "docker" }
func (Docker) Title() string             { return "Docker" }
func (Docker) BinSub() string            { return "" }
func (Docker) Smoke() (string, []string) { return "docker.exe", []string{"--version"} }
func (Docker) ProbeFiles() []string      { return []string{"docker.exe"} }

// docker/cli 仓库没有 GitHub Release，官方静态包只挂在 download.docker.com 目录页，
// 因此这里解析目录索引而不是走 ghBinaryReleases。
const dockerStaticBase = "https://download.docker.com/win/static/stable/x86_64"

var dockerZipRe = regexp.MustCompile(`docker-(\d+(?:\.\d+){2})\.zip`)

func (Docker) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	html, err := core.GetText(context.Background(), dockerStaticBase+"/")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []core.RemoteVersion
	for _, m := range dockerZipRe.FindAllStringSubmatch(html, -1) {
		ver := m[1]
		if seen[ver] {
			continue
		}
		seen[ver] = true
		archive := "docker-" + ver + ".zip"
		out = append(out, core.RemoteVersion{
			Version: ver, Size: -1,
			URL:     dockerStaticBase + "/" + archive,
			Archive: archive,
		})
	}
	sort.Slice(out, func(i, j int) bool { return core.CompareVersions(out[i].Version, out[j].Version) > 0 })
	if len(out) > 40 {
		out = out[:40]
	}
	return out, nil
}

// Redis —— Redis for Windows
type Redis struct{}

func (Redis) ID() string           { return "redis" }
func (Redis) Title() string        { return "Redis" }
func (Redis) BinSub() string       { return "" }
func (Redis) Smoke() (string, []string) { return "redis-cli.exe", []string{"--version"} }
func (Redis) ProbeFiles() []string { return []string{"redis-cli.exe", "redis-server.exe"} }

var redisAssetRe = regexp.MustCompile(`(?i)^Redis-x64-.*\.zip$`)

func (Redis) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return ghBinaryReleases(context.Background(),
		"https://api.github.com/repos/tporadowski/redis/releases?per_page=20",
		redisAssetRe, "redis")
}
