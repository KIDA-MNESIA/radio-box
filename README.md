# radio-box

基于 [sing-box](https://github.com/SagerNet/sing-box) 的修改版本，增加了额外的 DNS 功能。

## 新增功能

### DNS 上游竞速与超时控制

本版本在 `dns.rules` 的 `route` 动作中增加了对多上游并行竞速和超时/后备 DNS 的支持，类似于 AdGuard Home 的上游 DNS 行为。

#### 全局配置

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
| `upstream_timeout_ms` | uint32 | 全局上游 DNS 查询超时时间（毫秒）。`0` 表示使用默认超时。 |
| `fallback_timeout_ms` | uint32 | 全局后备 DNS 查询超时时间（毫秒）。`0` 将使用 `upstream_timeout_ms` 的值。 |
| `fallback_grace_ms` | uint32 | 启动后备 DNS 后，主上游仍可继续等待的宽限窗口（毫秒）。`0` 表示禁用宽限窗口。 |

#### 规则级别配置

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
| `server` | string \| []string | 目标 DNS 服务器标签。支持数组形式以实现多上游并行竞速。 |
| `fallback_dns` | string \| []string | 后备 DNS 服务器标签。当主上游超时触发时启动后备查询。 |
| `upstream_timeout_ms` | uint32 | 该规则的上游超时时间（毫秒），覆盖全局设置。 |
| `fallback_timeout_ms` | uint32 | 该规则的后备超时时间（毫秒），覆盖全局设置。 |
| `fallback_grace_ms` | uint32 | 该规则的宽限窗口（毫秒），覆盖全局设置。 |

### 行为说明

#### 多上游并行竞速

当 `server` 配置为数组时，将并发向所有服务器发起 DNS 查询：

- 优先使用第一个返回 `NOERROR` 的响应
- 如果全部服务器都返回非 `NOERROR`（如 `SERVFAIL`/`NXDOMAIN`），则使用最先返回的响应
- 一旦获得有效响应，立即取消其他查询

#### Hedged Fallback（对冲式后备）

当同时配置了 `upstream_timeout_ms` 和 `fallback_dns` 时，启用 hedged fallback 模式：

1. **T=0**: 向所有主上游服务器发起并行查询
2. **T=upstream_timeout_ms**: 如果主上游未返回有效响应，启动所有后备服务器的并行查询
3. **T=upstream_timeout_ms+fallback_grace_ms**: 主上游查询的最终截止时间
4. **T=upstream_timeout_ms+fallback_timeout_ms**: 后备查询的截止时间（从后备启动时计算）

这种设计确保：
- 主上游在超时后不会立即被放弃，仍有 grace 窗口
- 后备查询与主上游在 grace 窗口内竞争
- 最终选择最快返回的有效响应

#### 无后备 DNS 时的超时行为

当配置了 `upstream_timeout_ms` 但未配置 `fallback_dns` 时：

- 超时触发后立即返回（尽量返回已获得的最佳结果，否则返回超时错误）
- 不再继续等待慢速上游

### 配置示例

#### 示例 1：基础超时控制

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

#### 示例 2：多上游竞速

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

#### 示例 3：Hedged Fallback

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

### 路由 resolve 动作的 route_only

本版本为 `route.rules` 中的 `resolve` 动作新增 `route_only` 选项：

- 默认（`route_only: false`）：解析后会使用 IP 作为实际出站目标，服务器端看到的目标通常是 `IP:Port`。
- 启用（`route_only: true`）：解析得到的 IP **仅用于路由判定**（例如 `ip_cidr`），但出站仍会收到原始的 `Domain:Port` 作为目标。

配置示例：

```json
{
  "route": {
    "rules": [
      {
        "action": "resolve",
        "route_only": true
      }
    ]
  }
}
```

## 许可证

本项目基于 sing-box，遵循相同的开源许可证。