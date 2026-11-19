package tools

import (
	"context"
	"strings"

	"github.com/MUHUIX/envswitch/internal/core"
	"github.com/MUHUIX/envswitch/internal/settings"
)

// Go —— 官方 dl JSON（golang.google.cn 镜像同接口）。zip 根 go/ 展平后 bin/ 进 PATH。
type Golang struct{}

func (Golang) ID() string           { return "go" }
func (Golang) Title() string        { return "Go" }
func (Golang) BinSub() string       { return "bin" }
func (Golang) Smoke() (string, []string) { return "go.exe", []string{"version"} }

type goRelease struct {
	Version string   `json:"version"` // go1.26.5
	Stable  bool     `json:"stable"`
	Files   []goFile `json:"files"`
}

type goFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Sha256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

func (Golang) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	base := goGoogleCN
	switch s.Mirrors["go"] {
	case "official":
		base = goOfficial
	case "aliyun":
		base = goAliyun
	}
	var rels []goRelease
	if err := core.GetJSON(context.Background(), base+"/?mode=json&include=all", &rels); err != nil {
		return nil, err
	}
	var out []core.RemoteVersion
	for _, r := range rels {
		var win *goFile
		for i := range r.Files {
			f := &r.Files[i]
			if f.OS == "windows" && f.Arch == "amd64" && strings.HasSuffix(f.Filename, ".zip") {
				win = f
				break
			}
		}
		if win == nil {
			continue
		}
		label := ""
		if r.Stable {
			label = "stable"
		}
		var alts []string
		switch base {
		case goGoogleCN:
			alts = []string{goOfficial + "/" + win.Filename}
		case goOfficial:
			alts = []string{goGoogleCN + "/" + win.Filename}
		}
		out = append(out, core.RemoteVersion{
			Version: trimGo(r.Version), Label: label, Size: win.Size,
			URL: base + "/" + win.Filename, SHA256: win.Sha256, Archive: win.Filename,
			Alts: alts,
		})
	}
	return out, nil
}

func trimGo(v string) string { return strings.TrimPrefix(v, "go") }
