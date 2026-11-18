package tools

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HUIdada1/envswitch/internal/core"
	"github.com/HUIdada1/envswitch/internal/settings"
)

// Python —— python-build-standalone install_only（含 pip/venv，portable 解压即用）。
// 源三级：github(API) / nju(镜像目录页，国内直连) / auto（API 快探 6s，失败退 nju）。
type Python struct{}

func (Python) ID() string     { return "python" }
func (Python) Title() string  { return "python" }
func (Python) BinSub() string { return "" }
func (Python) Smoke() (string, []string) {
	return "python.exe", []string{"--version"}
}

var (
	pyAssetRe  = regexp.MustCompile(`^cpython-(\d+\.\d+\.\d+)\+\d+-x86_64-pc-windows-msvc-install_only\.tar\.gz$`)
	pyTagRe    = regexp.MustCompile(`href="(\d{8})/"`)
	pyAssetHre = regexp.MustCompile(`href="([^"]*x86_64-pc-windows-msvc-install_only\.tar\.gz)"`)
)

const pyNJUBase = "https://mirror.nju.edu.cn/github-release/astral-sh/python-build-standalone/"

func (Python) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	switch strings.ToLower(s.Mirrors["python"]) {
	case "github":
		return pyFromGitHub(context.Background())
	case "nju":
		return pyFromNJU(context.Background())
	default: // auto
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		vers, err := pyFromGitHub(ctx)
		cancel()
		if err == nil {
			return vers, nil
		}
		v2, err2 := pyFromNJU(context.Background())
		if err2 != nil {
			return nil, core.W(core.ErrNet, "GitHub 与 NJU 镜像均不可达：api=%v / mirror=%v（可在设置配 GitHub 代理前缀）", err, err2)
		}
		return v2, nil
	}
}

// pyFromGitHub —— releases API，digest 字段直供 sha256。
func pyFromGitHub(ctx context.Context) ([]core.RemoteVersion, error) {
	seen := map[string]bool{}
	var out []core.RemoteVersion
	for page := 1; page <= 2 && len(out) < 80; page++ {
		var rels []ghRelease
		url := pythonReleasesAPI + "?per_page=100&page=" + itoa(page)
		if err := core.GetJSON(ctx, url, &rels); err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		if len(rels) == 0 {
			break
		}
		for _, r := range rels {
			for _, a := range r.Assets {
				m := pyAssetRe.FindStringSubmatch(a.Name)
				if m == nil || seen[m[1]] {
					continue
				}
				seen[m[1]] = true
				sha := strings.TrimPrefix(a.Digest, "sha256:")
				date := ""
				if len(r.PublishedAt) >= 10 {
					date = r.PublishedAt[:10]
				}
				out = append(out, core.RemoteVersion{
					Version: m[1], Date: date, Size: a.Size,
					URL: a.BrowserDownloadURL, SHA256: sha, Archive: a.Name,
					Alts: []string{pyNJUBase + r.TagName + "/" + strings.ReplaceAll(a.Name, "+", "%2B")},
				})
			}
		}
	}
	return out, nil
}

// pyFromNJU —— 镜像目录页：第一级 release tag（日期），第二级资产。
func pyFromNJU(ctx context.Context) ([]core.RemoteVersion, error) {
	page, err := core.GetText(ctx, pyNJUBase)
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, m := range pyTagRe.FindAllStringSubmatch(page, -1) {
		tags = append(tags, m[1])
	}
	if len(tags) == 0 {
		return nil, core.W(core.ErrNet, "NJU 镜像目录无 release tag")
	}
	sort.Sort(sort.Reverse(sort.StringSlice(tags)))
	if len(tags) > 16 { // 覆盖近 ~1.5 年的构建（3.12/3.13/3.14/3.15 共存区间）
		tags = tags[:16]
	}

	type res struct {
		tag  string
		err  error
		vers []core.RemoteVersion
	}
	results := make([]res, len(tags))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, t := range tags {
		wg.Add(1)
		go func(i int, t string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			body, err := core.GetText(ctx, pyNJUBase+t+"/")
			if err != nil {
				results[i] = res{tag: t, err: err}
				return
			}
			var vers []core.RemoteVersion
			for _, m := range pyAssetHre.FindAllStringSubmatch(body, -1) {
				name := strings.ReplaceAll(m[1], "%2B", "+")
				mm := pyAssetRe.FindStringSubmatch(name)
				if mm == nil {
					continue
				}
				vers = append(vers, core.RemoteVersion{
					Version: mm[1], Date: tagDate(t), Size: -1,
					URL: pyNJUBase + t + "/" + name, Archive: name,
					Alts: []string{"https://github.com/astral-sh/python-build-standalone/releases/download/" + t + "/" + name},
				})
			}
			results[i] = res{tag: t, vers: vers}
		}(i, t)
	}
	wg.Wait()

	seen := map[string]bool{}
	var out []core.RemoteVersion
	failed := 0
	for _, r := range results {
		if r.err != nil {
			failed++
			continue
		}
		for _, v := range r.vers {
			if seen[v.Version] {
				continue // 同名版本取更早 tag 页（已按新→旧排，先到先得即最新）
			}
			seen[v.Version] = true
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, core.W(core.ErrNet, "NJU 镜像 %d 个 tag 页全部解析失败", failed)
	}
	sort.Slice(out, func(i, j int) bool { return core.CompareVersions(out[i].Version, out[j].Version) > 0 })
	return out, nil
}

func tagDate(t string) string {
	if len(t) == 8 {
		return t[0:4] + "-" + t[4:6] + "-" + t[6:8]
	}
	return ""
}

// —— GitHub releases API 通用结构（python 用；未来 pnpm standalone 可复用）——

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt string    `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"` // GitHub API: "sha256:<hex>"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
