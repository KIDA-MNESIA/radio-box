---
icon: material/new-box
---

!!! quote "sing-box 1.12.0 中的更改"

    :material-plus: [strategy](#strategy)  
    :material-plus: [predefined](#predefined)

!!! question "自 sing-box 1.11.0 起"

### route

```json
{
  "action": "route", // 默认
  "server": "",
  "fallback_dns": "",
  "upstream_timeout_ms": 0,
  "fallback_timeout_ms": 0,
  "fallback_grace_ms": 0,
  "strategy": "",
  "disable_cache": false,
  "rewrite_ttl": null,
  "client_subnet": null
}
```

`route` 继承了将 DNS 请求 路由到指定服务器的经典规则动作。

#### server

==必填==

目标 DNS 服务器的标签（字符串）或标签列表（数组）。

当配置为数组时，将并发向所有服务器发起 DNS 查询。

- 优先使用第一个返回 `NOERROR` 的响应。
- 如果全部服务器都返回非 `NOERROR`（例如 `SERVFAIL`/`NXDOMAIN`），则使用最先返回的那个响应。

#### upstream_timeout_ms

该规则动作的上游 DNS 查询超时时间（毫秒）。

将覆盖 `dns.upstream_timeout_ms`。

#### fallback_timeout_ms

该规则动作的后备 DNS 查询超时时间（毫秒）。

将覆盖 `dns.fallback_timeout_ms`；如果两者均为 `0`，则使用 `upstream_timeout_ms`。

#### fallback_grace_ms

在启动后备 DNS 查询之后，仍允许主服务器继续等待的宽限窗口（毫秒）。

将覆盖 `dns.fallback_grace_ms`。

#### fallback_dns

后备 DNS 服务器的标签（字符串）或标签列表（数组）。

当上游超时触发时，将启动后备 DNS 查询。

- 主服务器仍会继续运行额外的 `fallback_grace_ms`。
- 后备 DNS 将并发查询。

如果未配置 `fallback_dns`，则在超时触发时将立刻返回（不再继续等待）。

#### strategy

!!! question "自 sing-box 1.12.0 起"

为此查询设置域名策略。

可选项：`prefer_ipv4` `prefer_ipv6` `ipv4_only` `ipv6_only`。

#### disable_cache

在此查询中禁用缓存。

#### rewrite_ttl

重写 DNS 回应中的 TTL。

#### client_subnet

默认情况下，将带有指定 IP 前缀的 `edns0-subnet` OPT 附加记录附加到每个查询。

如果值是 IP 地址而不是前缀，则会自动附加 `/32` 或 `/128`。

将覆盖 `dns.client_subnet`.

### route-options

```json
{
  "action": "route-options",
  "disable_cache": false,
  "rewrite_ttl": null,
  "client_subnet": null
}
```

`route-options` 为路由设置选项。

### reject

```json
{
  "action": "reject",
  "method": "",
  "no_drop": false
}
```

`reject` 拒绝 DNS 请求。

#### method

- `default`: 返回 REFUSED。
- `drop`: 丢弃请求。

默认使用 `defualt`。

#### no_drop

如果未启用，则 30 秒内触发 50 次后，`method` 将被暂时覆盖为 `drop`。

当 `method` 设为 `drop` 时不可用。

### predefined

!!! question "自 sing-box 1.12.0 起"

```json
{
  "action": "predefined",
  "rcode": "",
  "answer": [],
  "ns": [],
  "extra": []
}
```

`predefined` 以预定义的 DNS 记录响应。

#### rcode

响应码。

| 值          | 旧 rcode DNS 服务器中的值 | 描述              |
|------------|--------------------|-----------------|
| `NOERROR`  | `success`          | Ok              |
| `FORMERR`  | `format_error`     | Bad request     |
| `SERVFAIL` | `server_failure`   | Server failure  |
| `NXDOMAIN` | `name_error`       | Not found       |
| `NOTIMP`   | `not_implemented`  | Not implemented |
| `REFUSED`  | `refused`          | Refused         |

默认使用 `NOERROR`。

#### answer

用于作为回答响应的文本 DNS 记录列表。

例子:

| 记录类型   | 例子                            |
|--------|-------------------------------|
| `A`    | `localhost. IN A 127.0.0.1`   |
| `AAAA` | `localhost. IN AAAA ::1`      |
| `TXT`  | `localhost. IN TXT \"Hello\"` |

#### ns

用于作为名称服务器响应的文本 DNS 记录列表。

#### extra

用于作为额外记录响应的文本 DNS 记录列表。
