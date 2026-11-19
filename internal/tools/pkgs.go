package tools

import (
	"context"

	"github.com/MUHUIX/envswitch/internal/core"
	"github.com/MUHUIX/envswitch/internal/settings"
)

// —— npm / pnpm / yarn：registry tarball 形态；junction 指包根，PATH 经自生成 shim ——

type Npm struct{}

func (Npm) ID() string     { return "npm" }
func (Npm) Title() string  { return "npm" }
func (Npm) BinSub() string { return "" }
func (Npm) Smoke() (string, []string) {
	return "bin/npm-cli.js", []string{"--version"}
}
func (Npm) Shims() map[string]string {
	return map[string]string{"npm": "bin/npm-cli.js", "npx": "bin/npx-cli.js"}
}
func (Npm) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return fetchNpmVersions(context.Background(), s, "npm")
}

type Pnpm struct{}

func (Pnpm) ID() string     { return "pnpm" }
func (Pnpm) Title() string  { return "pnpm" }
func (Pnpm) BinSub() string { return "" }
func (Pnpm) Smoke() (string, []string) {
	return "bin/pnpm.cjs", []string{"--version"}
}
func (Pnpm) Shims() map[string]string {
	return map[string]string{"pnpm": "bin/pnpm.cjs", "pnpx": "bin/pnpx.cjs"}
}
func (Pnpm) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return fetchNpmVersions(context.Background(), s, "pnpm")
}

type Yarn struct{}

func (Yarn) ID() string     { return "yarn" }
func (Yarn) Title() string  { return "yarn classic" }
func (Yarn) BinSub() string { return "" }
func (Yarn) Smoke() (string, []string) {
	return "bin/yarn.js", []string{"--version"}
}
func (Yarn) Shims() map[string]string {
	return map[string]string{"yarn": "bin/yarn.js"}
}
func (Yarn) FetchRemote(s *settings.Settings) ([]core.RemoteVersion, error) {
	return fetchNpmVersions(context.Background(), s, "yarn")
}
