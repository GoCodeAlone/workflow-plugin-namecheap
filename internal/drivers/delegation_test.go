package drivers

import (
	"context"
	"errors"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"
	"github.com/namecheap/go-namecheap-sdk/v2/namecheap"
)

type fakeDelegationClient struct {
	getList   func(domain string) (*namecheap.DomainsDNSGetListCommandResponse, error)
	setCustom func(domain string, nameservers []string) (*namecheap.DomainsDNSSetCustomCommandResponse, error)
}

func (f *fakeDelegationClient) GetList(domain string) (*namecheap.DomainsDNSGetListCommandResponse, error) {
	if f.getList != nil {
		return f.getList(domain)
	}
	return delegationListResponse(domain, []string{"dns1.registrar-servers.com", "dns2.registrar-servers.com"}), nil
}

func (f *fakeDelegationClient) SetCustom(domain string, nameservers []string) (*namecheap.DomainsDNSSetCustomCommandResponse, error) {
	if f.setCustom != nil {
		return f.setCustom(domain, nameservers)
	}
	updated := true
	return &namecheap.DomainsDNSSetCustomCommandResponse{
		DomainDNSSetCustomResult: &namecheap.DomainsDNSSetCustomResult{
			Domain:  &domain,
			Updated: &updated,
		},
	}, nil
}

func delegationListResponse(domain string, nameservers []string) *namecheap.DomainsDNSGetListCommandResponse {
	return &namecheap.DomainsDNSGetListCommandResponse{
		DomainDNSGetListResult: &namecheap.DomainDNSGetListResult{
			Domain:      &domain,
			Nameservers: &nameservers,
		},
	}
}

func TestDelegationDriver_ReadReturnsRegistrarNameservers(t *testing.T) {
	d := NewDelegationDriverWithClient(&fakeDelegationClient{})
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "example.com", Type: "infra.dns_delegation", ProviderID: "example.com"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Type != "infra.dns_delegation" || out.ProviderID != "example.com" {
		t.Fatalf("output identity = %#v", out)
	}
	got := out.Outputs["nameservers"].([]string)
	want := []string{"dns1.registrar-servers.com", "dns2.registrar-servers.com"}
	if !equalNameserverSets(got, want) {
		t.Fatalf("nameservers = %#v, want %#v", got, want)
	}
	authority := out.Outputs["authority"].(map[string]any)
	if !equalNameserverSets(authority["registrar_nameservers"].([]string), want) {
		t.Fatalf("authority = %#v", authority)
	}
}

func TestDelegationDriver_UpdateCallsSetCustom(t *testing.T) {
	var gotDomain string
	var gotNS []string
	d := NewDelegationDriverWithClient(&fakeDelegationClient{
		setCustom: func(domain string, nameservers []string) (*namecheap.DomainsDNSSetCustomCommandResponse, error) {
			gotDomain = domain
			gotNS = append([]string(nil), nameservers...)
			updated := true
			return &namecheap.DomainsDNSSetCustomCommandResponse{
				DomainDNSSetCustomResult: &namecheap.DomainsDNSSetCustomResult{Domain: &domain, Updated: &updated},
			}, nil
		},
	})
	spec := interfaces.ResourceSpec{
		Name: "example.com",
		Type: "infra.dns_delegation",
		Config: map[string]any{
			"domain":      "example.com",
			"nameservers": []any{"Mckinley.NS.Cloudflare.com.", "amos.ns.cloudflare.com"},
		},
	}
	out, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "example.com", Type: "infra.dns_delegation", ProviderID: "example.com"}, spec)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if gotDomain != "example.com" {
		t.Fatalf("SetCustom domain = %q", gotDomain)
	}
	want := []string{"amos.ns.cloudflare.com", "mckinley.ns.cloudflare.com"}
	if !equalNameserverSets(gotNS, want) {
		t.Fatalf("SetCustom nameservers = %#v, want %#v", gotNS, want)
	}
	if !equalNameserverSets(out.Outputs["nameservers"].([]string), want) {
		t.Fatalf("output nameservers = %#v", out.Outputs["nameservers"])
	}
}

func TestDelegationDriver_DiffIgnoresNameserverOrder(t *testing.T) {
	d := NewDelegationDriverWithClient(&fakeDelegationClient{})
	desired := interfaces.ResourceSpec{
		Name: "example.com",
		Type: "infra.dns_delegation",
		Config: map[string]any{
			"domain":      "example.com",
			"nameservers": []any{"mckinley.ns.cloudflare.com", "amos.ns.cloudflare.com"},
		},
	}
	current := delegationOutput("example.com", "example.com", []string{"amos.ns.cloudflare.com.", "mckinley.ns.cloudflare.com"})
	diff, err := d.Diff(context.Background(), desired, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff.NeedsUpdate || diff.NeedsReplace {
		t.Fatalf("diff = %#v, want no change", diff)
	}
}

func TestDelegationDriver_ValidationAndErrors(t *testing.T) {
	d := NewDelegationDriverWithClient(&fakeDelegationClient{})
	_, err := d.Create(context.Background(), interfaces.ResourceSpec{
		Name: "example.com",
		Type: "infra.dns_delegation",
		Config: map[string]any{
			"domain":      "example.com",
			"nameservers": []any{"one.nameserver.example"},
		},
	})
	if err == nil {
		t.Fatal("Create expected validation error for fewer than two nameservers")
	}

	err = d.Delete(context.Background(), interfaces.ResourceRef{Name: "example.com", Type: "infra.dns_delegation", ProviderID: "example.com"})
	if err == nil {
		t.Fatal("Delete expected refusal")
	}
}

func TestDelegationDriver_SetCustomFailure(t *testing.T) {
	d := NewDelegationDriverWithClient(&fakeDelegationClient{
		setCustom: func(_ string, _ []string) (*namecheap.DomainsDNSSetCustomCommandResponse, error) {
			return nil, errors.New("api failed")
		},
	})
	_, err := d.Update(context.Background(), interfaces.ResourceRef{Name: "example.com", Type: "infra.dns_delegation", ProviderID: "example.com"}, interfaces.ResourceSpec{
		Name: "example.com",
		Type: "infra.dns_delegation",
		Config: map[string]any{
			"domain":      "example.com",
			"nameservers": []any{"amos.ns.cloudflare.com", "mckinley.ns.cloudflare.com"},
		},
	})
	if err == nil {
		t.Fatal("Update expected SetCustom error")
	}
}
