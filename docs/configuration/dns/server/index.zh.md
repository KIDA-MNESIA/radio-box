---
icon: material/alert-decagram
---

!!! quote "sing-box 1.12.0 中的更改"

    :material-plus: [type](#type)

# DNS Server

### 结构

```json
{
  "dns": {
    "servers": [
      {
        "type": "",
        "tag": "",
        "client_subnet_from_inbound": null
      }
    ]
  }
}
```

#### type

DNS 服务器的类型。

| 类型              | 格式                        |
|-----------------|---------------------------|
| empty (default) | [Legacy](./legacy/)       |
| `local`         | [Local](./local/)         |
| `hosts`         | [Hosts](./hosts/)         |
| `tcp`           | [TCP](./tcp/)             |
| `udp`           | [UDP](./udp/)             |
| `tls`           | [TLS](./tls/)             |
| `quic`          | [QUIC](./quic/)           |
| `https`         | [HTTPS](./https/)         |
| `h3`            | [HTTP/3](./http3/)        |
| `dhcp`          | [DHCP](./dhcp/)           |
| `fakeip`        | [Fake IP](./fakeip/)      |
| `tailscale`     | [Tailscale](./tailscale/) |
| `resolved`      | [Resolved](./resolved/)   |

#### tag

DNS 服务器的标签。

#### client_subnet_from_inbound

从当前 DNS 请求对应的入站连接/会话的对端地址派生一个前缀，并以 `edns0-subnet` OPT 附加记录附加到查询。

格式：

- 数字：作为 IPv4 前缀长度（IPv6 不生效）。
- 对象：`{"ipv4": 24, "ipv6": 56}`。

将覆盖 `dns.client_subnet_from_inbound`。

优先级低于 `client_subnet`。
