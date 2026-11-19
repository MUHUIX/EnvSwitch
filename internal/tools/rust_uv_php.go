package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/MUHUIX/envswitch/internal/core"
	"github.com/MUHUIX/envswitch/internal/settings"
)

// Rust —— 官方独立便携工具链归档 (rustc + cargo x86_64-pc-windows-msvc/gnu)
type Rust struct{}

func (Rust) ID() string           { return "rust" }
func (Rust) Title() string        { return "Rust" }
func (Rust) BinSub() string       { return "bin" }
func (Rust) Smoke() (string, []string) { return "rustc.exe", []string{"--version"} }
func (Rust) ProbeFiles() []string { return []string{"bin/rustc.exe", "bin/cargo.exe"} }

// 官方静态分发归档：https://static.rust-lang.org/dist/rust-<ver>-x86_64-pc-windows-msvc.zip
const rustStaticBase = "https://static.rust-lang.org/dist"

func (Rust) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	// 从 GitHub API 获取稳定 Release 标签列表
	var rels []struct {
		TagName string `json:"tag_name"`
	}
	url := "https://api.github.com/repos/rust-lang/rust/releases?per_page=30"
	if err := core.GetJSON(context.Background(), url, &rels); err != nil {
		return nil, err
	}
	var out []core.RemoteVersion
	seen := map[string]bool{}
	for _, r := range rels {
		v := strings.TrimPrefix(r.TagName, "v")
		if v == "" || seen[v] || strings.Contains(v, "beta") || strings.Contains(v, "nightly") {
			continue
		}
		seen[v] = true
		archive := fmt.Sprintf("rust-%s-x86_64-pc-windows-msvc.zip", v)
		dl := fmt.Sprintf("%s/%s", rustStaticBase, archive)
		out = append(out, core.RemoteVersion{
			Version: v,
			Size:    -1,
			URL:     dl,
			Archive: archive,
		})
	}
	return out, nil
}

// Uv —— Astral 出品现代极速 Python 包与版本管理器
type Uv struct{}

func (Uv) ID() string           { return "uv" }
func (Uv) Title() string        { return "uv" }
func (Uv) BinSub() string       { return "" }
func (Uv) Smoke() (string, []string) { return "uv.exe", []string{"--version"} }
func (Uv) ProbeFiles() []string { return []string{"uv.exe"} }

func (Uv) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	var rels []struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"browser_download_url"`
			Size        int64  `json:"size"`
		} `json:"assets"`
	}
	url := "https://api.github.com/repos/astral-sh/uv/releases?per_page=30"
	if err := core.GetJSON(context.Background(), url, &rels); err != nil {
		return nil, err
	}
	var out []core.RemoteVersion
	for _, r := range rels {
		v := strings.TrimPrefix(r.TagName, "v")
		if v == "" {
			continue
		}
		for _, a := range r.Assets {
			if a.Name == "uv-x86_64-pc-windows-msvc.zip" {
				out = append(out, core.RemoteVersion{
					Version: v,
					Size:    a.Size,
					URL:     a.DownloadURL,
					Archive: a.Name,
				})
				break
			}
		}
	}
	return out, nil
}

// Php —— Windows 官方 x64 VS16 Thread Safe 便携归档
type Php struct{}

func (Php) ID() string           { return "php" }
func (Php) Title() string        { return "PHP" }
func (Php) BinSub() string       { return "" }
func (Php) Smoke() (string, []string) { return "php.exe", []string{"-v"} }
func (Php) ProbeFiles() []string { return []string{"php.exe"} }

func (Php) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	// 提供常见 PHP 8.x 稳定版本
	known := []string{"8.4.1", "8.3.14", "8.2.26", "8.1.31"}
	var out []core.RemoteVersion
	for _, v := range known {
		archive := fmt.Sprintf("php-%s-Win32-vs16-x64.zip", v)
		dl := fmt.Sprintf("https://windows.php.net/downloads/releases/archives/%s", archive)
		out = append(out, core.RemoteVersion{
			Version: v,
			Size:    -1,
			URL:     dl,
			Archive: archive,
		})
	}
	return out, nil
}
