# 第三方组件与商标声明

EnvSwitch 自身采用 [MIT License + 附加使用条款](./LICENSE) 的复合授权（非纯 MIT），禁止商业用途、二次分发牟利与改名换皮冒充官方。该协议**不改变**下列第三方组件的授权：它们在各自许可授予你的权利范围内，仍按各自许可使用，不受本项目附加条款约束。发生冲突时，仅就该组件而言以第三方许可为准。

## Go 依赖

### 直接依赖

| 模块 | 版本 | 许可 |
|------|------|------|
| github.com/spf13/cobra | v1.8.1 | Apache-2.0 |
| github.com/wailsapp/wails/v2 | v2.10.2 | MIT |
| golang.org/x/sys | v0.30.0 | BSD-3-Clause |

### 主要间接依赖

| 模块 | 版本 | 许可 |
|------|------|------|
| github.com/spf13/pflag | v1.0.5 | BSD-3-Clause |
| github.com/gorilla/websocket | v1.5.3 | BSD-2-Clause |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/samber/lo | v1.49.1 | MIT |
| github.com/bep/debounce | v1.2.1 | MIT |
| github.com/go-ole/go-ole | v1.3.0 | MIT |
| github.com/mattn/go-colorable | v0.1.13 | MIT |
| github.com/mattn/go-isatty | v0.0.20 | MIT |
| github.com/rivo/uniseg | v0.4.7 | MIT |
| github.com/labstack/echo/v4 | v4.13.3 | MIT |
| github.com/labstack/gommon | v0.4.2 | MIT |
| github.com/valyala/bytebufferpool | v1.0.0 | MIT |
| github.com/valyala/fasttemplate | v1.2.2 | MIT |
| github.com/wailsapp/go-webview2 | v1.0.19 | MIT |
| github.com/wailsapp/mimetype | v1.4.1 | MIT |
| github.com/leaanthony/go-ansi-parser | v1.6.1 | MIT |
| github.com/leaanthony/slicer | v1.6.0 | MIT |
| github.com/leaanthony/u | v1.1.1 | MIT |
| github.com/godbus/dbus/v5 | v5.1.0 | BSD-2-Clause |
| golang.org/x/crypto | v0.33.0 | BSD-3-Clause |
| golang.org/x/net | v0.35.0 | BSD-3-Clause |
| golang.org/x/text | v0.22.0 | BSD-3-Clause |

## 前端依赖

| 模块 | 版本 | 许可 |
|------|------|------|
| vue | ^3.4.38 | MIT |
| vite | ^4.5.5 | MIT |
| @vitejs/plugin-vue | ^4.6.2 | MIT |
| frontend/wailsjs（Wails 生成绑定） | 随 Wails v2 | MIT |

## 运行时组件（不由本项目分发）

- **Microsoft Edge WebView2 Runtime**：GUI 的渲染宿主，由微软提供，随 Windows 10/11 预装或由安装器引导安装，受微软软件许可条款约束，本项目不随包分发其二进制。
- **被管理的工具本体**：node、python、go、java 等由各自的官方发行渠道提供，下载后按其原始许可使用，本项目只负责落位与链接重指向，不改变其授权。

## 商标

`EnvSwitch` 名称、logo、应用图标与视觉识别系统是作者沐晖的商标与品牌资产，不在 [LICENSE](LICENSE) 的授权范围内，不得用于任何产品、服务、组织、域名或账号名称。

本项目管理或提及的下列名称均为其各自所有者的商标或注册商标，本项目与这些所有者之间不存在隶属、赞助或背书关系：

Node.js（OpenJS Foundation）、npm / npx（npm, Inc.）、pnpm、Yarn（Meta）、Python（PSF）、Go（Google）、Rust（Rust Foundation）、PHP（PHP Group）、Deno、Bun、uv（Astral）、Git（Software Freedom Conservancy）、nvm-windows（Corey Butler）、Java / JDK / Temurin（Oracle / Eclipse Foundation）、Maven（Apache Software Foundation）、Gradle（Gradle, Inc.）、.NET（Microsoft）、Docker（Docker, Inc.）、Redis（Redis Ltd.）、kubectl（CNCF / Linux Foundation）、Terraform（HashiCorp）、Windows（Microsoft）。

上述列举用于说明互不隶属的事实，不构成对任何商标权的主张。

## 核验完整清单

第三方依赖以 `go.mod` / `go.sum` 与 `frontend/package.json` 为唯一事实来源；上表为人工整理，可能与上游更新不同步，一律以上游仓库的 `LICENSE` 为准。需要完整清单时：

```bash
go list -m all                                  # 全部 Go 模块及版本
go run github.com/google/go-licenses@latest report ./...   # 汇总许可类型
cd frontend && npm ls --all                     # 全部前端依赖
npx license-checker --summary                   # 前端许可汇总
```

发现遗漏或标注有误，请提 [Issue](https://github.com/HUIdada1/EnvSwitch/issues)。
