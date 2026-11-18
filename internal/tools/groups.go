package tools

// 各工具的前端分组（侧栏按此渲染，组内顺序 = tools.All() 顺序）。
// 规范板块：
// 1. runtime: 语言运行时
// 2. packages: 包管理与工具链
// 3. enterprise: 企业运行时 (JVM/.NET)
// 4. cloud: 云原生与运维

func (Node) Group() string   { return "runtime" }
func (Python) Group() string { return "runtime" }
func (Golang) Group() string { return "runtime" }
func (Rust) Group() string   { return "runtime" }
func (Php) Group() string    { return "runtime" }
func (Deno) Group() string   { return "runtime" }
func (Bun) Group() string    { return "runtime" }

func (Npm) Group() string  { return "packages" }
func (Pnpm) Group() string { return "packages" }
func (Yarn) Group() string { return "packages" }
func (Pip) Group() string  { return "packages" }
func (Nvm) Group() string  { return "packages" }
func (Uv) Group() string   { return "packages" }
func (Git) Group() string  { return "packages" }

func (Java) Group() string   { return "enterprise" }
func (Maven) Group() string  { return "enterprise" }
func (Gradle) Group() string { return "enterprise" }
func (Dotnet) Group() string { return "enterprise" }

func (Docker) Group() string    { return "cloud" }
func (Kubectl) Group() string   { return "cloud" }
func (Terraform) Group() string { return "cloud" }
func (Redis) Group() string     { return "cloud" }
