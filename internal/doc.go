// Package internal implements workflow-plugin-namecheap, an external Workflow
// IaC provider for Namecheap-managed DNS zones and Namecheap domain transfers.
//
// The provider exposes the `iac.provider.namecheap` module and the `infra.dns`
// and `infra.domain_transfer` resource types. It is intended to run through
// Workflow's external plugin host; Go consumers usually reference it from a
// Workflow manifest instead of importing this package directly.
//
// Namecheap API requests require three provider configuration values:
// `api_user`, `api_key`, and `client_ip`. The `client_ip` value is not secret
// credential material, but it is required by Namecheap on every API request and
// must match an IP address already allowlisted in the Namecheap control panel.
// CIDR ranges are not accepted by the Namecheap API.
//
// The production integration uses github.com/namecheap/go-namecheap-sdk/v2.
// Tests should prefer the narrow fakeable driver interfaces in the drivers
// package instead of making live Namecheap calls.
//
// `infra.dns` manages the full host-record set for one domain because
// Namecheap exposes DNS changes through a whole-zone SetHosts operation. Applying
// a desired record set replaces the zone with exactly the declared records.
// `infra.domain_transfer` starts or observes domain transfers; creating a
// transfer requires explicit confirmation because it places a chargeable
// Namecheap order, and the EPP/auth code is never written to outputs.
package internal
