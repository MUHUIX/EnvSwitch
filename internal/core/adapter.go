package core

import "github.com/MUHUIX/envswitch/internal/settings"

// RemoteVersion 一条远程可安装版本。
type RemoteVersion struct {
	Version  string `json:"version"`
	Label    string `json:"label"`    // LTS / Current / ""
	Codename string `json:"codename"` // node: Jod 等
	Date     string `json:"date"`     // 2026-10-28
	Size     int64  `json:"size"`     // -1 未知
	URL      string `json:"url"`      // 最终下载地址（未加代理）
	SHA256    string `json:"sha256"`    // 已知哈希（空则走 SHASUMS 兜底或跳过强校验并提示）
	Integrity string `json:"integrity"` // npm 风格 "sha512-<base64>"；下载后校验用
	Archive   string `json:"archive"`   // 归档文件名
	Alts      []string `json:"alts,omitempty"` // 备用下载 URL（镜像 404/超时自动换源重试）
}

// Install 一条本机已安装/已检测版本。
type Install struct {
	Version  string `json:"version"`
	Source   string `json:"source"` // store / nvm / detected
	BinDir   string `json:"binDir"` // junction 应指向的目录（进 PATH 的那级）
	Size     int64  `json:"size"`   // -1 未知
	Active   bool   `json:"active"`
	Editable bool   `json:"editable"` // EnvSwitch store 内的版本才可删
}

// Adapter 一个工具的版本源与 store 布局约定。
// 约定：解包后如存在唯一顶层目录（node-vX-win-x64、go、python、package）则展平一层，
// store/<tool>/<ver>/ 即归档内容根；BinSub 给出根下进 PATH 的子路径（"" 为根）。
type Adapter interface {
	ID() string
	Title() string
	// Group 前端侧栏分组：runtime / packages / jvm / cloud / dotnet
	Group() string

	// FetchRemote 返回最新在前的版本列表（缓存由 registry.go 统一处理）。
	FetchRemote(s *settings.Settings) ([]RemoteVersion, error)

	// BinSub store 版本目录下进 PATH 的子目录：node/python/go/deno/bun/dotnet 为 ""，
	// npm/yarn/pnpm 为 "bin"，java 为 "bin"，maven/gradle/kubectl/terraform 见各自声明。
	BinSub() string

	// Smoke 冒烟：runtime 工具为 (BinDir 下可执行文件, 参数)；
	// JSRunner 为 (版本根相对入口 js, 参数)；返回 ("", nil) 表示不执行只查 ProbeFiles。
	Smoke() (exe string, args []string)
}

// Prober 声明"不执行、只验存在"的解包完整性标记（maven/gradle 等需要
// JAVA_HOME 的构建工具，冒烟交给用户环境，安装时至少验证目录骨架）。
type Prober interface {
	ProbeFiles() []string
}

// JSRunner 声明 npm/pnpm/yarn 这类 node 驱动的工具：
// junction 指向包根，PATH 上通过自生成 shim 调 node 跑固定入口脚本，
// 不依赖包内 .cmd（其假设 node_modules 安装布局）。
type JSRunner interface {
	// Shims 命令名 → 包根相对入口 js（如 "npm" → "bin/npm-cli.js"）。
	Shims() map[string]string
}

// FastAdopter 可选：总览轮询用的轻量收编列表（版本名与路径，不算目录大小）。
type FastAdopter interface {
	AdoptedFast(s *settings.Settings) []Install
}
