package tools

import (
	"context"
	"regexp"
	"strings"

	"github.com/HUIdada1/envswitch/internal/core"
	"github.com/HUIdada1/envswitch/internal/settings"
)

// version 从 tag 提取数字段（deno tag=v2.9.8，bun tag=bun-v1.4.4，redis tag=5.0.14.1）
var tagVer = regexp.MustCompile(`\d+(?:\.\d+)+`)

// deno / bun：GitHub release 独立二进制 zip（解包展平后根目录即 exe）。

type Deno struct{}

func (Deno) ID() string           { return "deno" }
func (Deno) Title() string        { return "Deno" }
func (Deno) BinSub() string       { return "" }
func (Deno) Smoke() (string, []string) { return "deno.exe", []string{"--version"} }

var denoAsset = regexp.MustCompile(`^deno-x86_64-pc-windows-msvc\.zip$`)

func (Deno) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return ghBinaryReleases(context.Background(),
		"https://api.github.com/repos/denoland/deno/releases?per_page=60",
		denoAsset, "deno")
}

type Bun struct{}

func (Bun) ID() string           { return "bun" }
func (Bun) Title() string        { return "Bun" }
func (Bun) BinSub() string       { return "" }
func (Bun) Smoke() (string, []string) { return "bun.exe", []string{"--version"} }

var bunAsset = regexp.MustCompile(`^bun-windows-x64\.zip$`)

func (Bun) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return ghBinaryReleases(context.Background(),
		"https://api.github.com/repos/oven-sh/bun/releases?per_page=60",
		bunAsset, "bun")
}

// ghBinaryReleases GitHub releases API → 单二进制资产的版本列表。
// version 从 tag 提取数字段；sha 用 API digest 字段；代理前缀由下载层统一处理。
func ghBinaryReleases(ctx context.Context, apiURL string, assetRe *regexp.Regexp, name string) ([]core.RemoteVersion, error) {
	var rels []ghRelease
	if err := core.GetJSON(ctx, apiURL, &rels); err != nil {
		return nil, err
	}
	var out []core.RemoteVersion
	for _, r := range rels {
		if r.Prerelease || (name == "bun" && strings.Contains(strings.ToLower(r.TagName), "canary")) {
			continue
		}
		// tag 前缀因仓库而异（v2.9.8 / bun-v1.4.4），统一提取数字段
		ver := tagVer.FindString(r.TagName)
		if ver == "" {
			continue
		}
		for _, a := range r.Assets {
			if !assetRe.MatchString(a.Name) {
				continue
			}
			date := ""
			if len(r.PublishedAt) >= 10 {
				date = r.PublishedAt[:10]
			}
			out = append(out, core.RemoteVersion{
				Version: ver, Date: date, Size: a.Size,
				URL: a.BrowserDownloadURL,
				SHA256: strings.TrimPrefix(a.Digest, "sha256:"),
				Archive: a.Name,
			})
		}
	}
	return out, nil
}
