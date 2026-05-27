//go:build live_dns

// Env-gated live integration coverage for EnumerateAll("infra.dns").
//
// Run with:
//
//	INFRA_DNS_ENUMERATE_LIVE=1 \
//	NAMECHEAP_API_USER=$USER \
//	NAMECHEAP_API_KEY=$KEY \
//	NAMECHEAP_CLIENT_IP=$IP \
//	  GOWORK=off go test -tags live_dns \
//	  -run TestNcProvider_EnumerateAll_DNS_live ./internal/...
//
// Namecheap's API requires an explicit allow-listed client IP in the
// account's sandbox/production whitelist — run from the self-hosted
// runner whose IP is registered there. Per
// docs/plans/2026-05-26-dns-provider-contract.md PR 3 (Task 9).
package internal

import (
	"context"
	"os"
	"testing"

	"github.com/namecheap/go-namecheap-sdk/v2/namecheap"
)

// newLiveNcProvider builds an ncProvider whose `domains` field is wired
// to the production namecheap.Client.Domains subservice. Credentials come
// from NAMECHEAP_API_USER + NAMECHEAP_API_KEY + NAMECHEAP_CLIENT_IP; the
// helper aborts the test (t.Fatal) when any are missing so the live-only
// run is loud rather than silent.
func newLiveNcProvider(t *testing.T) *ncProvider {
	t.Helper()
	user := os.Getenv("NAMECHEAP_API_USER")
	key := os.Getenv("NAMECHEAP_API_KEY")
	ip := os.Getenv("NAMECHEAP_CLIENT_IP")
	if user == "" || key == "" || ip == "" {
		t.Fatal("NAMECHEAP_API_USER + NAMECHEAP_API_KEY + NAMECHEAP_CLIENT_IP must be set for live EnumerateAll test")
	}
	sandbox := os.Getenv("NAMECHEAP_SANDBOX") == "1"
	client := namecheap.NewClient(&namecheap.ClientOptions{
		UserName:   user,
		ApiUser:    user,
		ApiKey:     key,
		ClientIp:   ip,
		UseSandbox: sandbox,
	})
	return &ncProvider{domains: client.Domains}
}

func TestNcProvider_EnumerateAll_DNS_live(t *testing.T) {
	if os.Getenv("INFRA_DNS_ENUMERATE_LIVE") != "1" {
		t.Skip("set INFRA_DNS_ENUMERATE_LIVE=1 + NAMECHEAP_API_USER + NAMECHEAP_API_KEY + NAMECHEAP_CLIENT_IP to run")
	}
	p := newLiveNcProvider(t)
	out, err := p.EnumerateAll(context.Background(), "infra.dns")
	if err != nil {
		t.Fatalf("live EnumerateAll: %v", err)
	}
	if len(out) == 0 {
		t.Skip("account has zero domains; cannot validate")
	}
	for _, o := range out {
		if o.ProviderID == "" {
			t.Errorf("empty ProviderID for %+v", o.Outputs)
		}
		if o.Type != "infra.dns" {
			t.Errorf("wrong Type %q", o.Type)
		}
		if _, ok := o.Outputs["zone"]; !ok {
			t.Errorf("missing zone output: %+v", o.Outputs)
		}
	}
	t.Logf("enumerated %d domains from live account", len(out))
}
