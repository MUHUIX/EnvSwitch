package tools

import (
	"context"
	"strings"

	"github.com/MUHUIX/envswitch/internal/core"
	"github.com/MUHUIX/envswitch/internal/settings"
)

type Node struct{}

func (Node) ID() string    { return "node" }
func (Node) Title() string { return "node.js" }
func (Node) BinSub() string { return "" }
func (Node) Smoke() (string, []string) { return "node.exe", []string{"--version"} }

type nodeIndexEntry struct {
	Version string   `json:"version"` // v22.22.0
	Date    string   `json:"date"`
	LTS     any      `json:"lts"` // false | "Jod"
	Files   []string `json:"files"`
}

func (n Node) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	base := nodeBase(s)
	var entries []nodeIndexEntry
	if err := core.GetJSON(context.Background(), base+"/index.json", &entries); err != nil {
		return nil, err
	}
	var out []core.RemoteVersion
	for _, e := range entries {
		if !hasStr(e.Files, "win-x64-zip") { // 索引以「平台-形态」命名
			continue
		}
		ver := strings.TrimPrefix(e.Version, "v")
		label := ""
		code := ""
		if s, ok := e.LTS.(string); ok && s != "" {
			label = "LTS"
			code = s
		}
		alt := nodeDistOfficial + "/" + e.Version + "/node-" + e.Version + "-win-x64.zip"
		if base != nodeDistOfficial {
			out = append(out, core.RemoteVersion{
				Version: ver, Label: label, Codename: code, Date: e.Date, Size: -1,
				URL: base + "/" + e.Version + "/node-" + e.Version + "-win-x64.zip", Archive: "node-" + e.Version + "-win-x64.zip",
				Alts: []string{alt},
			})
			continue
		}
		out = append(out, core.RemoteVersion{
			Version: ver, Label: label, Codename: code, Date: e.Date, Size: -1,
			URL: base + "/" + e.Version + "/node-" + e.Version + "-win-x64.zip", Archive: "node-" + e.Version + "-win-x64.zip",
			Alts: []string{nodeDistMirror + "/" + e.Version + "/node-" + e.Version + "-win-x64.zip"},
		})
	}
	return out, nil
}

// Adopted —— 收编 nvm-windows 已装版本（只读）。
func (Node) Adopted(s *settings.Settings) []core.Install { return core.NvmVersions() }

// AdoptedFast —— 总览轮询用：只列版本，不统计目录大小。
func (Node) AdoptedFast(s *settings.Settings) []core.Install { return core.NvmVersionsFast() }

func hasStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
