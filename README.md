# radio-box

基于 [sing-box](https://github.com/SagerNet/sing-box) 的修改版本，增加了一些 DNS 与路由相关的增强功能。

## 项目现状

- 模块路径仍为 `github.com/sagernet/sing-box`（与上游兼容）。
- 主要改动集中在 DNS 查询策略、路由 `resolve` 行为和策略组出站能力。
- 文档主入口为本文件。
- `test/` 为独立 Go module（见 `test/go.mod`），需单独执行集成测试。

## 快速开始

1. 初始化子模块（首次克隆后）：
   - `git submodule update --init --recursive`
2. 本地构建（推荐）：
   - `make build`
3. 启动服务（默认读取 `config.json`）：
   - `go run ./cmd/sing-box run`
4. 指定配置文件或目录运行：
   - `go run ./cmd/sing-box -c config.jsonc run`
   - `go run ./cmd/sing-box -C ./config run`

> `-c/--config` 支持 `.json` 与 `.jsonc`；`-C/--config-directory` 会读取目录下 `.json` 与 `.jsonc` 并按路径排序后合并。

## 开发常用命令

- 构建：`make build` / `make race`
- 配置检查：`go run ./cmd/sing-box -c config.jsonc check`
- 配置格式化：`go run ./cmd/sing-box -c config.jsonc format -w`
- 配置合并：`go run ./cmd/sing-box merge out.json -c config.json -C ./config`
- 主模块测试：`go test ./...`
- 集成测试：`cd test && go mod tidy && go test -v .`
- 一键测试：`make test` / `make test_stdio`
- 格式化：`make fmt`
- Lint：`make lint`

## 与上游差异总览

| 领域 | 本仓库状态 |
|------|------------|
| 配置解析 | 支持 JSONC（注释、尾逗号），含 `-c` 与 `-C` 场景 |
| DNS 规则路由 | `server` 支持数组并行竞速，新增 `fallback_dns` 与超时参数 |
| DNS ECS | 新增 `client_subnet_from_inbound`，并与缓存策略联动 |
| 路由 resolve | 新增 `route_only`、`fallback_to_final` |
| 策略组出站 | 新增 `fallback`、`load-balance`（含多策略） |

## 目录导航

- [新增功能](#新增功能)
- [1. 配置/规则集支持 JSONC（带注释与尾逗号）](#1-配置规则集支持-jsonc带注释与尾逗号)
- [2. DNS 上游竞速与超时控制](#2-dns-上游竞速与超时控制)
- [3. DNS：基于入站对端地址派生 edns0-subnet（ECS）与缓存隔离](#3-dns基于入站对端地址派生-edns0-subnetecs与缓存隔离)
- [4. 路由 resolve 动作增强（route_only / fallback_to_final）](#4-路由-resolve-动作增强route_only--fallback_to_final)
- [5. 出站组：自动回退（fallback）与负载均衡（load-balance）](#5-出站组自动回退fallback与负载均衡load-balance)
- [许可证](#许可证)

## 新增功能

### 1. 配置/规则集支持 JSONC（带注释与尾逗号）

本项目在读取配置与规则集 **JSON** 时，额外支持 **JSONC** 风格（`//`、`/* */` 注释，以及尾逗号）。

- 主配置文件可使用 `.json` 或 `.jsonc`（命令行 `-c/--config`）。
- 使用 `-C/--config-directory`（以及 rule-set merge 的目录读取）时，会同时扫描 `.json` 与 `.jsonc`。

极简示例（含注释与尾逗号）：

```jsonc
{
  /* block comment */
  "log": {
    // line comment
    "level": "info",
  },
}
```

### 2. DNS 上游竞速与超时控制

本版本在 `dns.rules` 的 `route` 动作中增加了对多上游并行竞速和超时/后备 DNS 的支持，类似于 AdGuard Home 的上游 DNS 行为。

#### 2.1 全局配置

在 `dns` 配置块中增加以下全局选项：

```json
{
  "dns": {
    "servers": [...],
    "rules": [...],
    "upstream_timeout_ms": 3000,
    "fallback_timeout_ms": 5000,
    "fallback_grace_ms": 500
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `upstream_timeout_ms` | uint32 | 全局上游 DNS 查询超时时间（毫秒）。`0` 表示不额外设置“路由层”超时（仍受 DNS 客户端默认超时约束）。 |
| `fallback_timeout_ms` | uint32 | 全局后备 DNS 查询超时时间（毫秒）。`0` 时继承 `upstream_timeout_ms`。 |
| `fallback_grace_ms` | uint32 | 启动后备 DNS 后，主上游仍可继续等待的宽限窗口（毫秒）。`0` 表示全局无宽限窗口。 |

#### 2.2 规则级别配置

在 `dns.rules[].route` 动作中增加以下选项：

```json
{
  "dns": {
    "rules": [
      {
        "domain_suffix": ["example.com"],
        "server": ["google", "cloudflare"],
        "fallback_dns": ["local"],
        "upstream_timeout_ms": 2000,
        "fallback_timeout_ms": 5000,
        "fallback_grace_ms": 300
      }
    ]
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `server` | string \| []string | 目标 DNS 服务器标签。支持数组形式实现多上游并行竞速。 |
| `fallback_dns` | string \| []string | 后备 DNS 服务器标签。当主上游超时触发时启动后备查询。 |
| `upstream_timeout_ms` | uint32 | 该规则的上游超时（毫秒）。`0` 时继承全局 `upstream_timeout_ms`。 |
| `fallback_timeout_ms` | uint32 | 该规则的后备超时（毫秒）。`0` 时继承全局 `fallback_timeout_ms`；若继承后仍为 `0`，再回退到 `upstream_timeout_ms`。 |
| `fallback_grace_ms` | uint32 | 该规则的宽限窗口（毫秒）。`0` 时继承全局 `fallback_grace_ms`。 |

#### 2.3 行为说明

- **超时参数优先级与 0 值规则**：
  - 规则级优先于全局级。
  - 规则级某项为 `0` 时，不是“关闭该功能”，而是“继承全局同名配置”。
  - `fallback_timeout_ms` 在规则级与全局级都为 `0` 时，会回退到最终生效的 `upstream_timeout_ms`。

- **多上游并行竞速**：当 `server` 配置为数组时，将并发向所有服务器发起 DNS 查询：
  - 优先使用第一个返回 `NOERROR` 的响应
  - 如果全部服务器都返回非 `NOERROR`（如 `SERVFAIL`/`NXDOMAIN`），则使用最先返回的响应
  - 一旦获得有效响应，立即取消其他查询

- **Hedged Fallback（对冲式后备）**：当同时配置了 `upstream_timeout_ms` 和 `fallback_dns` 时，启用 hedged fallback 模式：
  1. **T=0**：向所有主上游服务器发起并行查询
  2. **T=upstream_timeout_ms**：如果主上游未返回有效响应，启动所有后备服务器的并行查询
  3. **T=upstream_timeout_ms+fallback_grace_ms**：主上游查询的最终截止时间
  4. **T=upstream_timeout_ms+fallback_timeout_ms**：后备查询的截止时间（从后备启动时计算）

  这种设计确保：
  - 主上游在超时后不会立即被放弃，仍有 grace 窗口
  - 后备查询与主上游在 grace 窗口内竞争
  - 最终选择最快返回的有效响应

- **无后备 DNS 时的超时行为**：当配置了 `upstream_timeout_ms` 但未配置 `fallback_dns` 时：
  - 超时触发后立即返回（尽量返回已获得的最佳结果，否则返回超时错误）
  - 不再继续等待慢速上游

#### 2.4 配置示例

- **示例 1：基础超时控制**

```json
{
  "dns": {
    "servers": [
      { "tag": "google", "address": "tls://8.8.8.8" },
      { "tag": "local", "address": "223.5.5.5" }
    ],
    "rules": [
      {
        "domain_suffix": [".cn"],
        "server": "local"
      },
      {
        "server": "google",
        "upstream_timeout_ms": 3000
      }
    ],
    "upstream_timeout_ms": 5000
  }
}
```

- **示例 2：多上游竞速**

```json
{
  "dns": {
    "servers": [
      { "tag": "google", "address": "tls://8.8.8.8" },
      { "tag": "cloudflare", "address": "tls://1.1.1.1" },
      { "tag": "quad9", "address": "tls://9.9.9.9" }
    ],
    "rules": [
      {
        "server": ["google", "cloudflare", "quad9"]
      }
    ]
  }
}
```

- **示例 3：Hedged Fallback**

```json
{
  "dns": {
    "servers": [
      { "tag": "google", "address": "tls://8.8.8.8" },
      { "tag": "cloudflare", "address": "tls://1.1.1.1" },
      { "tag": "alidns", "address": "223.5.5.5" },
      { "tag": "dnspod", "address": "119.29.29.29" }
    ],
    "rules": [
      {
        "domain_suffix": [".com", ".net", ".org"],
        "server": ["google", "cloudflare"],
        "fallback_dns": ["alidns", "dnspod"],
        "upstream_timeout_ms": 2000,
        "fallback_timeout_ms": 3000,
        "fallback_grace_ms": 500
      }
    ]
  }
}
```

此配置含义：
- 对 `.com`/`.net`/`.org` 域名，首先并行查询 Google DNS 和 Cloudflare
- 如果 2 秒内未获得有效响应，同时启动 AliDNS 和 DNSPod 作为后备
- 主上游在 2.5 秒（2000+500ms）时完全截止
- 后备在 3 秒（从启动时计算）时截止
- 最终使用最先返回的有效响应

### 3. DNS：基于入站对端地址派生 `edns0-subnet`（ECS）与缓存隔离

本版本新增 `client_subnet_from_inbound` 配置项：当未配置任何显式 `client_subnet` 时，会从当前 DNS 请求对应的入站连接/会话的**对端地址**派生一个前缀，并以 `edns0-subnet` OPT 记录附加到上游查询。

> 例如：对端地址为 `59.110.9.191`，当设置 `ipv4: 24` 时，会派生为 `59.110.9.0/24`。

#### 3.1 全局配置

在 `dns` 配置块中增加以下选项：

```json
{
  "dns": {
    "client_subnet_from_inbound": { "ipv4": 24, "ipv6": 56 }
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `client_subnet_from_inbound` | number \| object | 从入站对端地址派生 ECS 前缀并附加到查询。数字表示 IPv4 前缀长度（IPv6 不生效）；对象格式为 `{ "ipv4": 24, "ipv6": 56 }`。 |

#### 3.2 Server 级别覆盖

在 `dns.servers[].client_subnet_from_inbound` 中设置可覆盖全局配置（但仍低于 `client_subnet`）：

```json
{
  "dns": {
    "servers": [
      {
        "tag": "cf",
        "address": "tls://1.1.1.1",
        "client_subnet_from_inbound": 24
      }
    ]
  }
}
```

#### 3.3 规则级别覆盖（dns.rules）

在 `dns.rules[]` 的 `route` / `route-options` 动作中也支持 `client_subnet_from_inbound`，优先级高于 server 级别与全局配置（但仍低于 `client_subnet`）：

```json
{
  "dns": {
    "rules": [
      {
        "domain_suffix": [".example.com"],
        "server": "cf",
        "client_subnet_from_inbound": { "ipv4": 24 }
      }
    ]
  }
}
```

#### 3.4 缓存行为（independent_cache）

当启用 `dns.independent_cache` 时，本版本会将本次查询附带的 `edns0-subnet` 前缀作为缓存 key 的一部分。

- `59.110.9.191/32` 与 `59.110.9.0/24` 会对应不同的缓存条目
- 这可以避免不同 ECS 维度下的结果互相污染

同时，未启用 `independent_cache` 时，带 ECS 的查询不会进入全局缓存，以避免跨子网错误复用。

### 4. 路由 resolve 动作增强（`route_only` / `fallback_to_final`）

本版本为 `route.rules` 中的 `resolve` 动作新增 `route_only` 与 `fallback_to_final` 选项，用于控制 **DNS 解析结果是否只用于“路由判定”，以及解析失败时是否回落到默认出站**。

#### 4.1 与默认行为的对比

- **默认（`route_only: false`，或省略）**：
  - `resolve` 解析域名后，会将“实际出站目标”改写为解析得到的 `IP:Port`
  - 因此上游（例如远端代理服务器）通常看到的是 `IP:Port`，而不是原始的 `Domain:Port`
  - 优点：避免域名在链路中丢失后还需要额外携带；并且上游不需要再解析域名

- **启用（`route_only: true`）**：
  - `resolve` 仍会进行 DNS 解析，但 **解析到的 IP 只用于路由判定**（例如命中 `ip_cidr` 规则）
  - “实际出站目标”保持为原始的 `Domain:Port`
  - 结果是：上游仍然能够拿到域名（便于记录/审计/服务端二次分流/由服务端 DNS 决定最终 IP）

#### 4.2 技术原理与工作机制

`resolve` 本质上是在路由阶段主动触发一次 DNS 解析，并将解析结果附加到当前连接/请求的路由元数据中，供后续规则进行匹配。

- 当 `route_only: false`（默认）时：
  - 解析结果不仅会进入“用于匹配的元数据”，还会被用于 **改写后续连接的目的地址**（目的地址从 `Domain:Port` 变为 `IP:Port`）

- 当 `route_only: true` 时：
  - 解析结果只进入“用于匹配的元数据”，**不改写目的地址**

因此，`route_only` 解决的是一个常见矛盾：
- 你希望“在本地用 IP 维度做精确分流”（必须先解析才能得到 IP）
- 同时又希望“让上游仍然看到域名”（不要把目的地址改写成 IP）

> 注意：启用 `route_only` 并不意味着只发生一次解析。
> 由于目的地址仍然是域名，上游（例如远端代理服务器）在建立到目标站点的连接时可能还会再次解析域名。

#### 4.3 适用场景举例

1. **服务端需要域名做二次路由/ACL**
   - 例如：服务端按域名做白名单/黑名单、记录审计日志、或做基于域名的分流策略。

2. **希望保留域名以便上游日志更可读**
   - 默认改写为 IP 后，上游日志/统计往往只剩 IP，排障时难以回溯到原始域名。

3. **客户端用 IP 做分流，但仍让服务端决定最终解析结果**
   - 例如：客户端仅用 `ip_cidr` 将内网/私网目标直连，其余走代理；但对外网目标仍希望由服务端 DNS 做最终解析（更贴近出口网络环境）。

#### 4.4 字段说明

以下字段位于 `route.rules[]` 的规则对象中，且仅在 `action: "resolve"` 时生效：

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `action` | string | （必填） | 固定为 `"resolve"`，表示执行一次 DNS 解析以辅助路由。 |
| `route_only` | bool | `false` | `false`：解析后将目的地址改写为 `IP:Port`；`true`：解析结果仅用于路由判定，不改写目的地址，出站仍为 `Domain:Port`。 |
| `fallback_to_final` | bool | `false` | 当本次 `resolve` 的 DNS 解析失败时，停止继续匹配后续路由规则并使用默认出站（`route.final`，未设置时为默认 DIRECT）。例外：若失败原因为 `DNS server not found`，不会触发该回落。 |

> 注：`fallback_to_final` 的设计受 Surge 的 `dns-failed` 行为启发。

#### 4.5 配置示例（不同场景对比）

- **示例 1：默认行为（省略 `route_only`）— 解析并改写为 `IP:Port`**

  ```json
  {
    "route": {
      "rules": [
        {
          "action": "resolve"
        }
      ]
    }
  }
  ```

  说明：`resolve` 执行后，后续实际出站目标通常会被改写为 IP。若你的上游需要看到域名（例如服务端做域名策略），这可能不符合预期。

- **示例 2：仅用于路由判定（`route_only: true`）— 上游仍收到 `Domain:Port`**

  ```json
  {
    "route": {
      "rules": [
        {
          "action": "resolve",
          "route_only": true
        },
        {
          "ip_cidr": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"],
          "outbound": "direct"
        },
        {
          "outbound": "proxy"
        }
      ]
    }
  }
  ```

  说明：
  - `resolve` 让域名请求也能命中 `ip_cidr` 规则（例如内网/私网直连）
  - 其余流量走 `proxy`，但因为 `route_only: true`，上游仍能看到原始域名

- **示例 3：只对特定域名启用 `route_only`（精细控制）**

  ```json
  {
    "route": {
      "rules": [
        {
          "domain_suffix": [".example.com"],
          "action": "resolve",
          "route_only": true
        },
        {
          "domain_suffix": [".example.com"],
          "outbound": "proxy"
        }
      ]
    }
  }
  ```

  说明：只对指定域名族启用“仅路由判定”的 resolve，避免对所有流量引入额外的解析开销。

- **示例 4：解析失败回落到默认出站（`fallback_to_final: true`）**

  ```json
  {
    "route": {
      "final": "proxy",
      "rules": [
        {
          "action": "resolve",
          "fallback_to_final": true
        },
        {
          "ip_cidr": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"],
          "outbound": "direct"
        },
        {
          "outbound": "proxy"
        }
      ]
    }
  }
  ```

  说明：当 `resolve` 解析失败时，将停止继续匹配后续规则，直接使用默认出站（即 `route.final` 对应的出站；未设置 `route.final` 时为默认 DIRECT 出站）。

### 5. 出站组：自动回退（fallback）与负载均衡（load-balance）

本版本新增两种策略组出站，行为参考 mihomo：
- 自动回退：[fallback](https://wiki.metacubex.one/config/proxy-groups/fallback/)
- 负载均衡：[load-balance](https://wiki.metacubex.one/config/proxy-groups/load-balance/)

#### 5.1 自动回退（fallback）

当当前出站连接失败时，会按 `outbounds` 顺序依次尝试下一个可用出站，直到成功。

支持健康检查（HEAD 请求）以更快地跳过不可用节点：当存在可用性记录时，会优先尝试“已被标记可用”的节点；若都没有记录，则按列表顺序逐个尝试。

配置示例：

```json
{
  "outbounds": [
    {
      "type": "fallback",
      "tag": "auto",
      "outbounds": ["ss-a", "ss-b", "direct"],
      "url": "https://www.gstatic.com/generate_204",
      "interval": "3m",
      "timeout": "5s",
      "idle_timeout": "30m",
      "interrupt_exist_connections": false
    }
  ]
}
```

#### 5.2 负载均衡（load-balance）

在 `outbounds` 中进行负载均衡选择，并在失败时自动重试切换到其他出站。

支持三种策略（`strategy`）：
- `round-robin`：按连接轮询分配到不同出站
- `consistent-hashing`：相同“目标地址”固定分配到同一出站（域名按 `eTLD+1` 归一）
- `sticky-sessions`：相同“来源地址 + 目标地址”固定分配到同一出站，缓存 10 分钟

配置示例：

```json
{
  "outbounds": [
    {
      "type": "load-balance",
      "tag": "lb",
      "outbounds": ["ss-a", "ss-b", "ss-c"],
      "strategy": "sticky-sessions",
      "url": "https://www.gstatic.com/generate_204",
      "interval": "3m",
      "timeout": "5s",
      "idle_timeout": "30m"
    }
  ]
}
```

#### 5.3 字段说明（fallback / load-balance 通用）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `outbounds` | []string | （必填） | 引用的出站 tag 列表（可包含其他组出站）。 |
| `url` | string | `https://www.gstatic.com/generate_204` | 健康检查 URL（使用 HEAD 请求）。 |
| `interval` | duration string | `3m` | 健康检查间隔。 |
| `timeout` | duration string | `15s` | 单次健康检查超时。 |
| `idle_timeout` | duration string | `30m` | 组长期未使用时停止周期检查。 |
| `interrupt_exist_connections` | bool | `false` | 当选择结果发生变化时是否中断已有外部连接，以加速切换。 |

> 约束：`interval` 必须小于等于 `idle_timeout`。

## 许可证

本项目基于 sing-box，遵循相同的开源许可证。
