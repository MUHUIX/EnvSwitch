// Package tools 是六个工具 + nvm 的适配器实现：远程索引解析、store 布局、冒烟命令。
package tools

import (
	"strings"

	"github.com/HUIdada1/envswitch/internal/core"
	"github.com/HUIdada1/envswitch/internal/settings"
)

func All() []core.Adapter {
	return []core.Adapter{
		Node{}, Python{}, Golang{}, Rust{}, Php{}, Deno{}, Bun{},
		Npm{}, Pnpm{}, Yarn{}, Uv{},
		Java{}, Maven{}, Gradle{},
		Dotnet{},
		Kubectl{}, Terraform{},
	}
}

func ByID(id string) core.Adapter {
	for _, a := range All() {
		if a.ID() == id {
			return a
		}
	}
	return nil
}

// —— 镜像基址表 ——

const (
	npmRegistryOfficial = "https://registry.npmjs.org"
	npmRegistryMirror   = "https://registry.npmmirror.com"
	nodeDistOfficial    = "https://nodejs.org/dist"
	nodeDistMirror      = "https://registry.npmmirror.com/-/binary/node"
	goOfficial          = "https://go.dev/dl"
	goGoogleCN          = "https://golang.google.cn/dl"
	goAliyun            = "https://mirrors.aliyun.com/golang"
	pythonReleasesAPI   = "https://api.github.com/repos/astral-sh/python-build-standalone/releases"
)

// regBase 按 settings.mirrors[<tool>] 决定 registry 基址。
func regBase(s *settings.Settings, tool string) string {
	switch strings.ToLower(s.Mirrors[tool]) {
	case "official", "npmjs":
		return npmRegistryOfficial
	case "custom":
		if u := s.Mirrors[tool+".url"]; strings.HasPrefix(u, "http") {
			return strings.TrimRight(u, "/")
		}
		return npmRegistryMirror
	default: // npmmirror
		return npmRegistryMirror
	}
}

func nodeBase(s *settings.Settings) string {
	switch strings.ToLower(s.Mirrors["node"]) {
	case "official":
		return nodeDistOfficial
	case "custom":
		if u := s.Mirrors["node.url"]; strings.HasPrefix(u, "http") {
			return strings.TrimRight(u, "/")
		}
		return nodeDistMirror
	default:
		return nodeDistMirror
	}
}
