# workflow-plugin-namecheap

[![CI](https://github.com/GoCodeAlone/workflow-plugin-namecheap/actions/workflows/ci.yml/badge.svg)](https://github.com/GoCodeAlone/workflow-plugin-namecheap/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Namecheap DNS and domain-transfer provider for the GoCodeAlone/workflow IaC surface.
Implements `infra.dns` and guarded `infra.domain_transfer` resources using the
[Namecheap API](https://www.namecheap.com/support/api/methods/)
via the [official Go SDK](https://github.com/namecheap/go-namecheap-sdk).

One `infra.dns` resource manages the **full record set** for one domain.
The Namecheap API uses a whole-zone `setHosts` call (no per-record
endpoints). The driver's `Diff()` method (called by the workflow engine
ahead of Apply) reads existing records via `GetHosts` and reports
`NeedsUpdate` when the desired set differs. When Apply runs,
`Create`/`Update` writes the full desired list in a single `setHosts`
call, replacing the zone wholesale.

Import reads Namecheap's current hosts and stores record metadata plus
`email_type` and `is_using_our_dns` in Workflow state. That authority metadata
is useful when adopting domains before a migration: a domain can be registered
at Namecheap without actually using Namecheap authoritative DNS.

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

## Domain Transfers

`infra.domain_transfer` starts and tracks a transfer into Namecheap. Creating a
transfer places a chargeable Namecheap order, so `confirm_transfer: true` is
required. The EPP/auth code is sent to Namecheap but is never stored in outputs.

```yaml
resources:
  - name: example-com-transfer
    type: infra.domain_transfer
    config:
      provider: namecheap
      domain: example.com
      years: 1
      epp_code: ${EXAMPLE_COM_EPP_CODE}
      confirm_transfer: true
      # optional
      promotion_code: SAVE
      add_free_whoisguard: true
      wg_enabled: true
```

Namecheap's API currently requires `years: 1` for transfer creation and only
supports a limited TLD set for API transfers. Use `wfctl import` with resource
type `infra.domain_transfer` and provider ID set to the transfer ID to track an
existing transfer's status.

## Required secrets

| Name | Sensitive | Source |
|------|-----------|--------|
| `NAMECHEAP_API_KEY` | **yes** | Profile → Tools → Namecheap API Access |

## Required configuration

| Name | Sensitive | Source |
|------|-----------|--------|
| `NAMECHEAP_API_USER` | no | Namecheap account username (= ApiUser in the API) |
| `NAMECHEAP_CLIENT_IP` | no | Public IP sent as Namecheap `ClientIp`; must already be allowlisted in the same control panel |

`NAMECHEAP_CLIENT_IP` is intentionally non-sensitive, but it is still required.
Namecheap requires every API request to include the caller's public `ClientIp`
and rejects requests unless that IP also appears in the account API allowlist.
Entering the IP in the Namecheap UI does not remove the need to pass the same
value in Workflow provider config.

```sh
wfctl secrets setup --plugin workflow-plugin-namecheap
wfctl vars setup --plugin workflow-plugin-namecheap
```

## Supported record types

`A`, `AAAA`, `ALIAS`, `CAA`, `CNAME`, `MX`, `MXE`, `NS`, `TXT`,
`URL`, `URL301`, `FRAME`

## Go Integration Notes

The runtime entrypoint is `cmd/workflow-plugin-namecheap`, which serves
`internal.NewIaCServer` through Workflow's external plugin host. Application code
usually references this plugin from a Workflow manifest with
`iac.provider.namecheap`; direct Go imports are mainly useful for provider
tests.

The production implementation uses
`github.com/namecheap/go-namecheap-sdk/v2`. DNS tests can exercise the provider
without live credentials by using the narrow fakeable interfaces in
`internal/drivers`. Live tests require `NAMECHEAP_API_USER`,
`NAMECHEAP_API_KEY`, and `NAMECHEAP_CLIENT_IP`, where `NAMECHEAP_CLIENT_IP`
matches the public IP allowlisted in the Namecheap account.

`infra.dns` uses Namecheap's whole-zone `SetHosts` API. The desired records in
Workflow become the complete host-record set for the domain.
`infra.domain_transfer` uses Namecheap transfer APIs and keeps EPP/auth codes
out of state outputs.

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
    config:
      provider: namecheap
      domain:   site-a.com
      records: [ { type: A, name: "@", data: 203.0.113.10, ttl: 1800 } ]
  - name: site-b
    type: infra.dns
    config:
      provider: namecheap
      domain:   site-b.org
      records: [ { type: A, name: "@", data: 203.0.113.20, ttl: 1800 } ]
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
