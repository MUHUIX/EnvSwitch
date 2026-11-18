package tools

import (
	"context"
	"regexp"
	"strings"

	"github.com/HUIdada1/envswitch/internal/core"
	"github.com/HUIdada1/envswitch/internal/settings"
)

// Pip —— Python 官方包管理器
type Pip struct{}

func (Pip) ID() string           { return "pip" }
func (Pip) Title() string        { return "pip" }
func (Pip) BinSub() string       { return "" }
func (Pip) Smoke() (string, []string) { return "pip.exe", []string{"--version"} }
func (Pip) ProbeFiles() []string { return []string{"pip.exe"} }

var pipVerRe = regexp.MustCompile(`^\d+\.\d+(?:\.\d+)?$`)

func (Pip) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	type pypiResp struct {
		Releases map[string][]struct {
			Filename string `json:"filename"`
			URL      string `json:"url"`
			Size     int64  `json:"size"`
			PackType string `json:"packagetype"`
		} `json:"releases"`
	}
	var resp pypiResp
	url := "https://pypi.org/pypi/pip/json"
	if err := core.GetJSON(context.Background(), url, &resp); err != nil {
		return []core.RemoteVersion{
			{Version: "24.0", Size: -1, URL: "https://pypi.org/packages/source/p/pip/pip-24.0.tar.gz", Archive: "pip-24.0.tar.gz"},
			{Version: "23.3.2", Size: -1, URL: "https://pypi.org/packages/source/p/pip/pip-23.3.2.tar.gz", Archive: "pip-23.3.2.tar.gz"},
			{Version: "23.2.1", Size: -1, URL: "https://pypi.org/packages/source/p/pip/pip-23.2.1.tar.gz", Archive: "pip-23.2.1.tar.gz"},
		}, nil
	}
	var out []core.RemoteVersion
	for ver, files := range resp.Releases {
		if !pipVerRe.MatchString(ver) {
			continue
		}
		for _, f := range files {
			if strings.HasSuffix(f.Filename, ".whl") || strings.HasSuffix(f.Filename, ".tar.gz") {
				out = append(out, core.RemoteVersion{
					Version: ver,
					Size:    f.Size,
					URL:     f.URL,
					Archive: f.Filename,
				})
				break
			}
		}
	}
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
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
	return ghBinaryReleases(context.Background(),
		"https://api.github.com/repos/coreybutler/nvm-windows/releases?per_page=20",
		nvmAssetRe, "nvm")
}

// Git —— Git for Windows
type Git struct{}

func (Git) ID() string           { return "git" }
func (Git) Title() string        { return "Git" }
func (Git) BinSub() string       { return "cmd" }
func (Git) Smoke() (string, []string) { return "git.exe", []string{"--version"} }
func (Git) ProbeFiles() []string { return []string{"cmd/git.exe", "bin/git.exe"} }

var gitAssetRe = regexp.MustCompile(`(?i)^PortableGit-.*-64-bit\.7z\.exe$`)

func (Git) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return ghBinaryReleases(context.Background(),
		"https://api.github.com/repos/git-for-windows/git/releases?per_page=25",
		gitAssetRe, "git")
}

// Docker —— Docker CLI
type Docker struct{}

func (Docker) ID() string           { return "docker" }
func (Docker) Title() string        { return "Docker" }
func (Docker) BinSub() string       { return "" }
func (Docker) Smoke() (string, []string) { return "docker.exe", []string{"--version"} }
func (Docker) ProbeFiles() []string { return []string{"docker.exe"} }

var dockerAssetRe = regexp.MustCompile(`(?i)^docker-.*\.zip$`)

func (Docker) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return ghBinaryReleases(context.Background(),
		"https://api.github.com/repos/docker/cli/releases?per_page=25",
		dockerAssetRe, "docker")
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
