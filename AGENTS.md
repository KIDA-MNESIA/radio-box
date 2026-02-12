# AGENTS.md

本文件为通用 Coding Agent 协作指南，适用于在本仓库中进行开发、排障与文档维护。

## 项目定位

- 本仓库是对 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 的修改版本（`go.mod` 模块路径仍为 `github.com/sagernet/sing-box`）。
- 主要新增能力集中在 DNS 与路由：
  - DNS 多上游并行竞速。
  - 上游超时触发后备 DNS（hedged fallback + grace window）。
  - `client_subnet_from_inbound`（基于入站对端地址派生 ECS）。
  - 路由 `resolve` 动作增强（`route_only` / `fallback_to_final`）。
  - 出站组 `fallback` 与 `load-balance`。

## 仓库事实速览

- 文档主入口：`README.md`。
- `test/` 是独立 Go module（见 `test/go.mod`），不包含在根目录 `go test ./...` 的覆盖范围内。
- 客户端子模块：`clients/android`、`clients/apple`（见 `.gitmodules`）。
  - 初始化命令：`git submodule update --init --recursive`

## 常用命令（构建 / 运行 / 测试 / 格式化 / Lint）

> 优先使用仓库内权威入口：`Makefile` 与 `cmd/sing-box/*`。

### 构建

- 推荐（复用默认 build tags）：
  - `make build`
  - `make race`
- 直接 Go 构建（等价示例）：
  - `go build ./cmd/sing-box`
  - `go build -tags "with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale" ./cmd/sing-box`

### 运行 / 配置检查（CLI）

CLI 入口在 `cmd/sing-box/`，全局 flags 在 `cmd/sing-box/cmd.go`：

- `-c/--config`：配置文件（可多次传入，支持 `.json` 与 `.jsonc`）。
- `-C/--config-directory`：配置目录（读取并合并目录下 `.json` 与 `.jsonc`）。
- `-D/--directory`：工作目录。

常用命令：

- `go run ./cmd/sing-box run`
- `go run ./cmd/sing-box -c config.jsonc run`
- `go run ./cmd/sing-box -C ./config run`
- `go run ./cmd/sing-box -c config.jsonc check`
- `go run ./cmd/sing-box -c config.jsonc format -w`
- `go run ./cmd/sing-box merge out.json -c config.json -C ./config`

### 测试

- 主 module：
  - `go test ./...`
  - `go test ./dns -run TestName`
- `test/` 子 module：
  - `cd test && go mod tidy && go test -v .`
- Makefile 集成目标：
  - `make test`
  - `make test_stdio`

### 代码格式化

- 安装工具：`make fmt_install`
- 执行格式化：`make fmt`
  - 包含 `gofumpt` + `gofmt -s` + `gci`。

### Lint

- 安装：`make lint_install`
- 执行：`make lint`
  - 按不同 `GOOS` 运行 `golangci-lint run ./...`。
  - 配置文件：`.golangci.yml`。

## 代码架构（大图）

### 1) 入口：CLI -> 配置解析 -> 运行实例

- `cmd/sing-box/main.go` -> `mainCommand.Execute()`。
- `cmd/sing-box/cmd.go`：全局 flag 初始化；未传 `-c/-C` 时默认读取 `config.json`。
- `cmd/sing-box/cmd_run.go`：
  - `readConfig()`：读取 `-c` 文件与 `-C` 目录下 `*.json` / `*.jsonc`。
  - `readConfigAndMerge()`：`badjson.MergeJSON(...)` 合并配置。
  - `create()`：`box.New(...)` -> `Box.Start()`。

### 2) 核心容器：`box.Box`

`box.go` 负责组装与启动子系统：

- manager 注册：`endpoint/inbound/outbound`、`dns`、`route`、`adapter/service`。
- 按 `experimental` 配置启用附加服务（cache-file、clash-api、v2ray-api 等）。
- 通过 `adapter.Start(...)` 分阶段启动（Initialize/Start/PostStart/Started）。

### 3) 配置层：`option/`

- 根配置：`option.Options`。
- DNS 全局字段：`option/dns.go` 中 `DNSClientOptions`。
- DNS 规则动作扩展：`option/rule_action.go` 中 `DNSRouteActionOptions`。
- 路由 `resolve` 扩展：`option/rule_action.go` 中 `RouteActionResolve`。

### 4) DNS 子系统：`dns/router.go`

- `matchDNS(...)` 将规则与全局参数折叠为查询计划。
- 并行竞速：`exchangeRacer(...)` / `lookupRacer(...)`。
- Hedged fallback：`exchangeHedgedRacer(...)` / `lookupHedgedRacer(...)`。
  - 在 `upstream_timeout` 触发后启动后备查询。
  - 主查询在 `fallback_grace` 窗口内仍继续等待。
  - 优先第一个 `NOERROR` 响应；否则回退到最先返回结果。

## DNS 改动联动检查清单

修改 DNS 竞速/超时/后备逻辑时，至少联动检查以下位置：

- 配置 schema：`option/dns.go`、`option/rule_action.go`。
- 规则动作映射：`route/rule/rule_action.go`。
- 运行时执行：`dns/router.go`、`dns/client.go`。
- 路由 resolve 行为：`route/route.go`、`route/conn.go`。
- 用户文档：`README.md`（本仓库主文档来源）。

## 外部/改造引入目录（改动前先确认来源）

- `transport/simple-obfs/`：来自 Clash 的修改版（见其目录内 README，且在 `.golangci.yml` 中被排除）。
- `common/ja3/`：JA3 相关代码（见其目录内 README）。
