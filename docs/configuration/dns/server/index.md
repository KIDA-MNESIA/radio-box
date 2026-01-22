---
icon: material/alert-decagram
---

!!! quote "Changes in sing-box 1.12.0"

    :material-plus: [type](#type)

# DNS Server

### Structure

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

The type of the DNS server.

| Type            | Format                    |
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

The tag of the DNS server.

#### client_subnet_from_inbound

Derive a prefix from the peer address of the inbound connection/session for the current DNS request, and append it as an `edns0-subnet` OPT extra record.

Format:

- Number: IPv4 prefix length (IPv6 disabled).
- Object: `{"ipv4": 24, "ipv6": 56}`.

Overrides `dns.client_subnet_from_inbound`.

Lower priority than `client_subnet`.
