# WARP.md

This file provides guidance to WARP (warp.dev) when working with code in this repository.

## 项目定位
- 本仓库是对 `SagerNet/sing-box` 的修改版本（`go.mod` 模块路径仍为 `github.com/sagernet/sing-box`），主要增加了额外的 DNS 功能：多上游并行竞速、以及超时触发后备 DNS（hedged fallback + grace window）。
- DNS 新增字段在 `README.md` 与 `docs/configuration/dns/`（含 `.zh.md`）有说明；对应的 JSON option 定义在 `option/`。

## 常用命令（构建 / 运行 / 测试 / Lint / 格式化）
> 下面优先给出“仓库内权威入口”（Makefile/CLI 子命令）。在 Windows/pwsh 下，如果没有 `make`，可直接用等价的 `go ...` 命令。

### 构建（开发态）
- 使用 Makefile（推荐，复用默认 build tags）：
  - `make build`
  - `make race`

- 直接用 Go（示例：PowerShell）：
  - `go build ./cmd/sing-box`
  - 带默认 tags 构建（与 `Makefile`/CI 接近）：
    - `go build -tags "with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale" ./cmd/sing-box`

### 运行/配置检查（CLI）
CLI 入口在 `cmd/sing-box/`，全局 flags 见 `cmd/sing-box/cmd.go`：
- `-c/--config`：配置文件（可多次传入）
- `-C/--config-directory`：配置目录（目录下 `.json` 会被读取并合并）
- `-D/--directory`：工作目录

常用：
- 查看帮助：`go run ./cmd/sing-box --help`
- 运行：
  - 默认会尝试读取 `config.json`：`go run ./cmd/sing-box run`
  - 指定配置文件：`go run ./cmd/sing-box -c config.json run`
  - 指定配置目录：`go run ./cmd/sing-box -C ./config run`
- 配置校验（只做构建/初始化，不启动服务）：`go run ./cmd/sing-box -c config.json check`
- 配置格式化（`-w` 写回源文件）：`go run ./cmd/sing-box -c config.json format -w`
- 合并配置输出：`go run ./cmd/sing-box merge out.json -c config.json -C ./config`

### 测试
注意：`test/` 目录是一个**独立 Go module**（`test/go.mod`），所以在仓库根目录执行的 `go test ./...` 默认不会覆盖 `test/` 下的测试。

- 仓库主 module（快速/常规）：
  - 全量：`go test ./...`
  - 运行单个测试（示例）：
    - 指定包：`go test ./dns -run TestName`
    - 全仓库匹配：`go test ./... -run TestName`

- `test/` 子模块（更偏集成/端到端）：
  - `cd test; go mod tidy; go test -v .`
  - `Makefile` 的对应目标：`make test`、`make test_stdio`

### 代码格式化
- 安装格式化工具：`make fmt_install`
- 执行格式化：`make fmt`
  - 使用 `gofumpt` + `gofmt -s` + `gci`（见 `Makefile`）

### Lint
- 安装：`make lint_install`
- 执行：`make lint`
  - `Makefile` 会用不同 `GOOS` 跑多次 `golangci-lint run ./...`
  - Lint 配置在 `.golangci.yml`（包含 build-tags 与排除目录）

### 文档（mkdocs）
- 站点配置：`mkdocs.yml`，内容在 `docs/`（含英文与 `*.zh.md` 的简中版本）。
- `Makefile`：
  - `make docs`（本地预览）
  - `make docs_install`（创建 venv 并安装 mkdocs 依赖；路径更偏类 Unix，Windows 下可能需要自行调整 venv 激活与脚本路径）

### Git submodules（客户端）
本仓库包含客户端子模块：`clients/android`、`clients/apple`（见 `.gitmodules`）。
- 初始化：`git submodule update --init --recursive`

## 代码架构（大图）
### 1) 入口：CLI → 配置解析 → 运行实例
- CLI 入口：`cmd/sing-box/main.go` → `mainCommand.Execute()`
- 全局初始化与 flag：`cmd/sing-box/cmd.go`
  - 默认没有传 `-c/-C` 时，会使用 `config.json`
- 配置读取/合并与启动：`cmd/sing-box/cmd_run.go`
  - `readConfig()`：读取 `-c` 文件与 `-C` 目录下的 `*.json`
  - `readConfigAndMerge()`：通过 `badjson.MergeJSON(...)` 合并多份配置
  - `create()`：`box.New(box.Options{Context, Options})` → `Box.Start()`

### 2) 核心容器：`box.Box` 负责组装各子系统
`box.go` 是运行时的“组装中心”，主要做：
- 构造并注册各种 manager（通过 `service.MustRegister[...]` 注入到 context）：
  - `endpoint.Manager` / `inbound.Manager` / `outbound.Manager`
  - `dns.TransportManager` + `dns.Router`
  - `route.NetworkManager` / `route.ConnectionManager` / `route.Router`
  - `adapter/service.Manager`
- 根据 `experimental` 配置选择性启用内部服务（如 cache-file、clash-api、v2ray-api 等）
- 通过 `adapter.Start(...)` 分阶段控制启动顺序（Initialize/Start/PostStart/Started）

### 3) 配置层：`option/` 定义 JSON schema（DNS 新字段也在这里）
- 全量配置根结构：`option.Options`（在 `cmd_run.go` 中反序列化）
- DNS 全局超时/后备参数：`option/dns.go` → `DNSClientOptions`
  - `upstream_timeout_ms` / `fallback_timeout_ms` / `fallback_grace_ms`
- DNS 规则动作扩展：`option/rule_action.go` → `DNSRouteActionOptions`
  - `server` 支持数组（并行竞速）
  - `fallback_dns` 支持数组（超时后并行后备）
  - per-rule 的超时字段覆盖全局

### 4) DNS 子系统：Transport 管理 + Router 匹配规则并执行查询
- Transport：`dns/transport_manager.go` + `dns/transport/*.go`
  - 通过 tag 管理上游（UDP/TCP/TLS/HTTPS/QUIC/...），供规则引用
- Router：`dns/router.go`
  - `matchDNS(...)`：把规则动作里的 server/fallback_dns +（规则/全局）超时参数折叠成一次查询计划
  - 并行竞速：`exchangeRacer(...)` / `lookupRacer(...)`
  - Hedged fallback（本仓库新增核心逻辑）：
    - `exchangeHedgedRacer(...)` / `lookupHedgedRacer(...)`
    - 在 `upstream_timeout` 触发后启动后备查询，同时主上游会在 `fallback_grace` 内继续等待
    - 优先选择第一个 `NOERROR` 响应；若都非 `NOERROR` 则使用最先返回者

### 5) “DNS 新功能”改动点的联动关系（改代码时常一起动）
当你修改 DNS 竞速/超时/后备行为时，通常需要同时检查这些位置是否一致：
- 配置 schema：`option/dns.go`、`option/rule_action.go`
- 规则动作映射：`route/rule/rule_action.go`（把 option 变成运行时 RuleActionDNSRoute）
- 执行逻辑：`dns/router.go`
- 文档：`docs/configuration/dns/index.md` / `index.zh.md`、`docs/configuration/dns/rule_action.md` / `rule_action.zh.md`

### 6) 外部/改造引入的代码目录（尽量少动，或先确认来源）
- `transport/simple-obfs/`：来自 Clash 的修改版（见其 README），在 `.golangci.yml` 中也被排除
- `common/ja3/`：JA3 相关代码（见其 README）