# workflow-plugin-namecheap

[![CI](https://github.com/GoCodeAlone/workflow-plugin-namecheap/actions/workflows/ci.yml/badge.svg)](https://github.com/GoCodeAlone/workflow-plugin-namecheap/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Namecheap DNS provider for the GoCodeAlone/workflow IaC surface.
Implements the `infra.dns` resource type using the
[Namecheap API](https://www.namecheap.com/support/api/methods/)
via the [official Go SDK](https://github.com/namecheap/go-namecheap-sdk).

One `infra.dns` resource manages the **full record set** for one domain.
The Namecheap API uses a whole-zone `setHosts` call (no per-record
endpoints). The driver's `Diff()` method (called by the workflow engine
ahead of Apply) reads existing records via `GetHosts` and reports
`NeedsUpdate` when the desired set differs. When Apply runs,
`Create`/`Update` writes the full desired list in a single `setHosts`
call, replacing the zone wholesale.

## Configuration

```yaml
modules:
  - name: namecheap
    type: iac.provider.namecheap
    config:
      api_user:  ${NAMECHEAP_API_USER}
      api_key:   ${NAMECHEAP_API_KEY}
      client_ip: ${NAMECHEAP_CLIENT_IP}
      # sandbox: true    # optional — point at api.sandbox.namecheap.com

resources:
  - name: gocodealone-tech
    type: infra.dns
    config:
      provider: namecheap
      domain:   gocodealone.tech
      records:
        - { type: A,     name: "@",   data: 203.0.113.10,      ttl: 1800 }
        - { type: CNAME, name: www,   data: gocodealone.tech., ttl: 1800 }
        - { type: MX,    name: "@",   data: mail.example.com., ttl: 1800, mx: 10 }
        - { type: TXT,   name: "@",   data: "v=spf1 include:_spf.example.com ~all", ttl: 300 }
```

## Required secrets

| Name | Sensitive | Source |
|------|-----------|--------|
| `NAMECHEAP_API_USER` | no | Namecheap account username (= ApiUser in the API) |
| `NAMECHEAP_API_KEY` | **yes** | Profile → Tools → Namecheap API Access |
| `NAMECHEAP_CLIENT_IP` | no | Public IP of the wfctl runner — must be allowlisted at the same control panel |

```sh
wfctl secrets setup --plugin workflow-plugin-namecheap
```

## Supported record types

`A`, `AAAA`, `ALIAS`, `CAA`, `CNAME`, `MX`, `MXE`, `NS`, `TXT`,
`URL`, `URL301`, `FRAME`

## Multi-domain accounts

Each `infra.dns` resource manages **one** domain's full record
set. To manage multiple domains in a single Namecheap account,
declare one resource per domain — all sharing the same
`iac.provider.namecheap` module (credentials are not repeated):

```yaml
modules:
  - name: namecheap
    type: iac.provider.namecheap
    config:
      api_user:  ${NAMECHEAP_API_USER}
      api_key:   ${NAMECHEAP_API_KEY}
      client_ip: ${NAMECHEAP_CLIENT_IP}

resources:
  - name: site-a
    type: infra.dns
    config: { provider: namecheap, domain: site-a.com, records: [...] }
  - name: site-b
    type: infra.dns
    config: { provider: namecheap, domain: site-b.org, records: [...] }
  - name: site-c
    type: infra.dns
    config: { provider: namecheap, domain: site-c.io,  records: [...] }
```

This is the intentional shape — keeping one zone per resource
makes Plan output unambiguous (you see exactly which zone is
changing), keeps the whole-zone `setHosts` semantics tractable,
and prevents `recordKey` collisions across domains.

## Caveats

- **Single IP allowlist**: Namecheap does not support CIDR. CI runners
  with rotating egress IPs need a NAT gateway or a static egress proxy.
- **API quota**: Namecheap rate-limits at 20 req/min per IP. The driver
  batches all record changes into one `setHosts` call per apply.
- **Replace semantics**: `setHosts` replaces the full zone. Any record
  not present in the desired set is dropped on apply. The
  `infra.dns` resource therefore manages the *entire* zone — do not
  mix wfctl-managed records with records configured by hand in the
  Namecheap UI; the next apply will delete the latter.
- **sandbox mode**: Set `sandbox: true` in the module config to target
  `api.sandbox.namecheap.com` for testing.

## Development

```sh
GOWORK=off go build ./...
GOWORK=off go test ./... -race -count=1
```
