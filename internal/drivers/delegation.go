package drivers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/GoCodeAlone/workflow/interfaces"
	"github.com/namecheap/go-namecheap-sdk/v2/namecheap"
)

type DelegationClient interface {
	GetList(domain string) (*namecheap.DomainsDNSGetListCommandResponse, error)
	SetCustom(domain string, nameservers []string) (*namecheap.DomainsDNSSetCustomCommandResponse, error)
}

type realDelegationClient struct{ svc *namecheap.DomainsDNSService }

func (r *realDelegationClient) GetList(domain string) (*namecheap.DomainsDNSGetListCommandResponse, error) {
	return r.svc.GetList(domain)
}

func (r *realDelegationClient) SetCustom(domain string, nameservers []string) (*namecheap.DomainsDNSSetCustomCommandResponse, error) {
	return r.svc.SetCustom(domain, nameservers)
}

type DelegationDriver struct {
	client DelegationClient
}

func NewDelegationDriver(c *namecheap.Client) *DelegationDriver {
	return &DelegationDriver{client: &realDelegationClient{svc: c.DomainsDNS}}
}

func NewDelegationDriverWithClient(c DelegationClient) *DelegationDriver {
	return &DelegationDriver{client: c}
}

func (d *DelegationDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dns delegation create %q: %w", spec.Name, err)
	}
	domain, nameservers, err := parseDelegationSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("dns delegation create %q: %w", spec.Name, err)
	}
	if err := d.setCustom(ctx, spec.Name, domain, nameservers); err != nil {
		return nil, err
	}
	return delegationOutput(spec.Name, domain, nameservers), nil
}

func (d *DelegationDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dns delegation read %q: %w", ref.Name, err)
	}
	domain := ref.ProviderID
	if domain == "" {
		domain = ref.Name
	}
	resp, err := d.client.GetList(domain)
	if err != nil {
		return nil, fmt.Errorf("dns delegation read %q: %w", ref.Name, err)
	}
	return delegationOutput(ref.Name, domain, nameserversFromGetList(resp)), nil
}

func (d *DelegationDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dns delegation update %q: %w", ref.Name, err)
	}
	currentDomain := ref.ProviderID
	if currentDomain == "" {
		currentDomain = ref.Name
	}
	domain, nameservers, err := parseDelegationSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("dns delegation update %q: %w", ref.Name, err)
	}
	if !strings.EqualFold(domain, currentDomain) {
		return nil, fmt.Errorf("dns delegation update %q: spec.domain %q does not match current %q — domain change requires resource replace, not update", ref.Name, domain, currentDomain)
	}
	if err := d.setCustom(ctx, ref.Name, currentDomain, nameservers); err != nil {
		return nil, err
	}
	return delegationOutput(ref.Name, currentDomain, nameservers), nil
}

func (d *DelegationDriver) Delete(_ context.Context, ref interfaces.ResourceRef) error {
	return fmt.Errorf("dns delegation delete %q: refusing to reset Namecheap nameservers implicitly", ref.Name)
}

func (d *DelegationDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	desiredDomain, desiredNS, err := parseDelegationSpec(desired)
	if err != nil {
		return nil, fmt.Errorf("dns delegation diff: parse desired: %w", err)
	}
	if current.ProviderID != "" && !strings.EqualFold(desiredDomain, current.ProviderID) {
		return &interfaces.DiffResult{
			NeedsReplace: true,
			Changes: []interfaces.FieldChange{{
				Path:     "domain",
				Old:      current.ProviderID,
				New:      desiredDomain,
				ForceNew: true,
			}},
		}, nil
	}
	currentNS := nameserversFromOutputs(current.Outputs)
	if equalNameserverSets(currentNS, desiredNS) {
		return &interfaces.DiffResult{}, nil
	}
	return &interfaces.DiffResult{
		NeedsUpdate: true,
		Changes: []interfaces.FieldChange{{
			Path: "nameservers",
			Old:  currentNS,
			New:  desiredNS,
		}},
	}, nil
}

func (d *DelegationDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	if _, err := d.Read(ctx, ref); err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: true, Message: "ok"}, nil
}

func (d *DelegationDriver) Scale(ctx context.Context, ref interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return d.Read(ctx, ref)
}

func (d *DelegationDriver) Type() string { return "infra.dns_delegation" }

func (d *DelegationDriver) SensitiveKeys() []string { return nil }

func (d *DelegationDriver) ProviderIDFormat() interfaces.ProviderIDFormat {
	return interfaces.IDFormatDomainName
}

func (d *DelegationDriver) setCustom(ctx context.Context, resourceName, domain string, nameservers []string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("dns delegation update %q: %w", resourceName, err)
	}
	resp, err := d.client.SetCustom(domain, nameservers)
	if err != nil {
		return fmt.Errorf("dns delegation update %q: %w", resourceName, err)
	}
	if resp == nil || resp.DomainDNSSetCustomResult == nil || resp.DomainDNSSetCustomResult.Updated == nil || !*resp.DomainDNSSetCustomResult.Updated {
		return fmt.Errorf("dns delegation update %q: Namecheap did not confirm nameserver update", resourceName)
	}
	return nil
}

func parseDelegationSpec(spec interfaces.ResourceSpec) (string, []string, error) {
	domain, _ := spec.Config["domain"].(string)
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = strings.TrimSpace(spec.Name)
	}
	if domain == "" {
		return "", nil, fmt.Errorf("domain is required")
	}
	nameservers := nameserversFromConfig(spec.Config["nameservers"])
	if len(nameservers) < 2 {
		return "", nil, fmt.Errorf("nameservers must contain at least two entries")
	}
	return strings.ToLower(strings.TrimSuffix(domain, ".")), nameservers, nil
}

func nameserversFromConfig(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return normalizeNameservers(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return normalizeNameservers(out)
	default:
		return nil
	}
}

func nameserversFromGetList(resp *namecheap.DomainsDNSGetListCommandResponse) []string {
	if resp == nil || resp.DomainDNSGetListResult == nil || resp.DomainDNSGetListResult.Nameservers == nil {
		return nil
	}
	return normalizeNameservers(*resp.DomainDNSGetListResult.Nameservers)
}

func nameserversFromOutputs(outputs map[string]any) []string {
	raw, ok := outputs["nameservers"]
	if !ok {
		return nil
	}
	return nameserversFromConfig(raw)
}

func normalizeNameservers(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		ns := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if ns != "" {
			seen[ns] = true
		}
	}
	out := make([]string, 0, len(seen))
	for ns := range seen {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

func equalNameserverSets(a, b []string) bool {
	a = normalizeNameservers(a)
	b = normalizeNameservers(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func delegationOutput(name, domain string, nameservers []string) *interfaces.ResourceOutput {
	nameservers = normalizeNameservers(nameservers)
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.dns_delegation",
		ProviderID: domain,
		Outputs: map[string]any{
			"domain":      domain,
			"nameservers": nameservers,
			"authority": map[string]any{
				"registrar":             "Namecheap",
				"registrar_nameservers": nameservers,
			},
		},
		Status: "active",
	}
}
