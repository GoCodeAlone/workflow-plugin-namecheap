# workflow-plugin-namecheap

[![CI](https://github.com/GoCodeAlone/workflow-plugin-namecheap/actions/workflows/ci.yml/badge.svg)](https://github.com/GoCodeAlone/workflow-plugin-namecheap/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> 🧪 **Experimental** — DNS provider for the GoCodeAlone/workflow IaC surface.
> Not yet validated in production. APIs may shift before v1.0.

Namecheap DNS driver for the `infra.dns` resource type. Implements the
[ResourceDriver](https://github.com/GoCodeAlone/workflow/blob/main/interfaces/iac_resource_driver.go)
interface via the [official Namecheap SDK](https://github.com/namecheap/go-namecheap-sdk).

## Configuration

```yaml
modules:
  - name: namecheap
    type: iac.provider.namecheap
    config:
      api_user: ${NAMECHEAP_API_USER}
      api_key:  ${NAMECHEAP_API_KEY}
      client_ip: ${NAMECHEAP_CLIENT_IP}
      # sandbox: true                # optional — point at api.sandbox.namecheap.com

resources:
  - name: gocodealone-tech
    type: infra.dns
    config:
      provider: namecheap
      domain: gocodealone.tech
      records:
        - { type: A,     name: '@',   data: 203.0.113.10, ttl: 1800 }
        - { type: CNAME, name: 'www', data: gocodealone.tech., ttl: 1800 }
```

## Required secrets

| Name | Sensitive | Source |
|------|-----------|--------|
| `NAMECHEAP_API_USER` | no | Namecheap username (= ApiUser per the API) |
| `NAMECHEAP_API_KEY` | **yes** | Profile → Tools → Namecheap API Access |
| `NAMECHEAP_CLIENT_IP` | no | Public IP of the wfctl runner. Namecheap requires every IP that uses the API to be allowlisted at the same control panel |

`wfctl secrets setup --plugin workflow-plugin-namecheap` prompts for each.

## Caveats

- **Single IP allowlist**: Namecheap doesn't support CIDR. CI runners
  with rotating outbound IPs need either a NAT gateway or a static
  egress proxy.
- **API quota**: Namecheap rate-limits at 20 req/min per IP. The driver
  batches record edits into a single `setHosts` call per apply to stay
  well under.
- **Replace semantics**: `setHosts` is a full-replace API call — the
  driver reads existing records first and merges only the diff.

## Development

```sh
GOWORK=off go build ./...
GOWORK=off go test ./... -race -count=1
```
