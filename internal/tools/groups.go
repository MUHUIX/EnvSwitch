package tools

// 各工具的前端分组（侧栏按此渲染，组内顺序 = tools.All() 顺序）。
func (Node) Group() string   { return "runtime" }
func (Python) Group() string { return "runtime" }
func (Golang) Group() string { return "runtime" }
func (Deno) Group() string   { return "runtime" }
func (Bun) Group() string    { return "runtime" }

func (Npm) Group() string   { return "packages" }
func (Pnpm) Group() string  { return "packages" }
func (Yarn) Group() string  { return "packages" }

func (Java) Group() string   { return "jvm" }
func (Maven) Group() string  { return "jvm" }
func (Gradle) Group() string { return "jvm" }

func (Dotnet) Group() string { return "dotnet" }

func (Kubectl) Group() string    { return "cloud" }
func (Terraform) Group() string  { return "cloud" }
