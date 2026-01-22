---
icon: material/new-box
---

!!! quote "Changes in sing-box 1.12.0"

    :material-plus: [strategy](#strategy)  
    :material-plus: [predefined](#predefined)

!!! question "Since sing-box 1.11.0"

### route

```json
{
  "action": "route",  // default
  "server": "",
  "fallback_dns": "",
  "upstream_timeout_ms": 0,
  "fallback_timeout_ms": 0,
  "fallback_grace_ms": 0,
  "strategy": "",
  "disable_cache": false,
  "rewrite_ttl": null,
  "client_subnet": null,
  "client_subnet_from_inbound": null
}
```

`route` inherits the classic rule behavior of routing DNS requests to the specified server.

#### server

==Required==

Tag (string) or list of tags (array) of target server(s).

When an array is provided, DNS queries will be sent to all servers concurrently.

- Prefer the first response with `NOERROR`.
- If all servers return non-`NOERROR` (e.g. `SERVFAIL`/`NXDOMAIN`), use the first returned response.

#### upstream_timeout_ms

Per-rule upstream DNS query timeout in milliseconds.

Overrides `dns.upstream_timeout_ms`.

#### fallback_timeout_ms

Per-rule fallback DNS query timeout in milliseconds.

Overrides `dns.fallback_timeout_ms`. If both are `0`, uses `upstream_timeout_ms`.

#### fallback_grace_ms

Per-rule grace window in milliseconds for primary servers after fallback servers are started.

Overrides `dns.fallback_grace_ms`.

#### fallback_dns

Tag (string) or list of tags (array) of fallback server(s).

Fallback servers are started when the upstream timeout triggers.

- Primary servers will still run for an extra `fallback_grace_ms`.
- Fallback servers will be queried concurrently.

If `fallback_dns` is empty, the query will return as soon as the upstream timeout triggers.

#### strategy

!!! question "Since sing-box 1.12.0"

Set domain strategy for this query.

One of `prefer_ipv4` `prefer_ipv6` `ipv4_only` `ipv6_only`.

#### disable_cache

Disable cache and save cache in this query.

#### rewrite_ttl

Rewrite TTL in DNS responses.

#### client_subnet

Append a `edns0-subnet` OPT extra record with the specified IP prefix to every query by default.

If value is an IP address instead of prefix, `/32` or `/128` will be appended automatically.

Will overrides `dns.client_subnet`.

#### client_subnet_from_inbound

If `client_subnet` is not set, derive a prefix from the peer address of the inbound connection/session for the current DNS request, and append it as an `edns0-subnet` OPT extra record.

Format:

- Number: IPv4 prefix length (IPv6 disabled).
- Object: `{"ipv4": 24, "ipv6": 56}`.

Lower priority than `client_subnet`.

Will override `dns.client_subnet_from_inbound`.

### route-options

```json
{
  "action": "route-options",
  "disable_cache": false,
  "rewrite_ttl": null,
  "client_subnet": null,
  "client_subnet_from_inbound": null
}
```

`route-options` set options for routing.

### reject

```json
{
  "action": "reject",
  "method": "",
  "no_drop": false
}
```

`reject` reject DNS requests.

#### method

- `default`: Reply with REFUSED.
- `drop`: Drop the request.

`default` will be used by default.

#### no_drop

If not enabled, `method` will be temporarily overwritten to `drop` after 50 triggers in 30s.

Not available when `method` is set to drop.

### predefined

!!! question "Since sing-box 1.12.0"

```json
{
  "action": "predefined",
  "rcode": "",
  "answer": [],
  "ns": [],
  "extra": []
}
```

`predefined` responds with predefined DNS records.

#### rcode

The response code.

| Value      | Value in the legacy rcode server | Description     |
|------------|----------------------------------|-----------------|
| `NOERROR`  | `success`                        | Ok              |
| `FORMERR`  | `format_error`                   | Bad request     |
| `SERVFAIL` | `server_failure`                 | Server failure  |
| `NXDOMAIN` | `name_error`                     | Not found       |
| `NOTIMP`   | `not_implemented`                | Not implemented |
| `REFUSED`  | `refused`                        | Refused         |

`NOERROR` will be used by default.

#### answer

List of text DNS record to respond as answers.

Examples:

| Record Type | Example                       |
|-------------|-------------------------------|
| `A`         | `localhost. IN A 127.0.0.1`   |
| `AAAA`      | `localhost. IN AAAA ::1`      |
| `TXT`       | `localhost. IN TXT \"Hello\"` |

#### ns

List of text DNS record to respond as name servers.

#### extra

List of text DNS record to respond as extra records.
