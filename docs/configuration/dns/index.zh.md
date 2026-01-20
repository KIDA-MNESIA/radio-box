---
icon: material/alert-decagram
---

!!! quote "sing-box 1.12.0 中的更改"

    :material-decagram: [servers](#servers)

!!! quote "sing-box 1.11.0 中的更改"

    :material-plus: [cache_capacity](#cache_capacity)

# DNS

### 结构

```json
{
  "dns": {
    "servers": [],
    "rules": [],
    "final": "",
    "strategy": "",
    "upstream_timeout_ms": 0,
    "fallback_timeout_ms": 0,
    "fallback_grace_ms": 0,
    "disable_cache": false,
    "disable_expire": false,
    "independent_cache": false,
    "cache_capacity": 0,
    "reverse_mapping": false,
    "client_subnet": "",
    "fakeip": {}
  }
}

```

### 字段

| 键        | 格式                      |
|----------|-------------------------|
| `server` | 一组 [DNS 服务器](./server/) |
| `rules`  | 一组 [DNS 规则](./rule/)    |

#### final

默认 DNS 服务器的标签。

默认使用第一个服务器。

#### strategy

默认解析域名策略。

可选值: `prefer_ipv4` `prefer_ipv6` `ipv4_only` `ipv6_only`。

#### upstream_timeout_ms

全局上游 DNS 查询超时时间（毫秒）。

`0` 使用默认超时时间。

#### fallback_timeout_ms

全局后备 DNS 查询超时时间（毫秒）。

`0` 将使用 `dns.upstream_timeout_ms`。

#### fallback_grace_ms

在启动后备 DNS 查询之后，仍允许主服务器继续等待的宽限窗口（毫秒）。

`0` 禁用宽限窗口。

#### disable_cache

禁用 DNS 缓存。

#### disable_expire

禁用 DNS 缓存过期。

#### independent_cache

使每个 DNS 服务器的缓存独立，以满足特殊目的。如果启用，将轻微降低性能。

#### cache_capacity

!!! question "自 sing-box 1.11.0 起"

LRU 缓存容量。

小于 1024 的值将被忽略。

#### reverse_mapping

在响应 DNS 查询后存储 IP 地址的反向映射以为路由目的提供域名。

由于此过程依赖于应用程序在发出请求之前解析域名的行为，因此在 macOS 等 DNS 由系统代理和缓存的环境中可能会出现问题。

#### client_subnet

!!! question "自 sing-box 1.9.0 起"

默认情况下，将带有指定 IP 前缀的 `edns0-subnet` OPT 附加记录附加到每个查询。

如果值是 IP 地址而不是前缀，则会自动附加 `/32` 或 `/128`。

可以被 `servers.[].client_subnet` 或 `rules.[].client_subnet` 覆盖。

#### fakeip

[FakeIP](./fakeip/) 设置。
