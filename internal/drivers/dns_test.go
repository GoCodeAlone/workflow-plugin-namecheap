package drivers

import (
	"context"
	"errors"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"
	"github.com/namecheap/go-namecheap-sdk/v2/namecheap"
)

// fakeDNSClient implements DNSClient for tests.
type fakeDNSClient struct {
	getHosts func(domain string) (*namecheap.DomainsDNSGetHostsCommandResponse, error)
	setHosts func(args *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error)
}

func (f *fakeDNSClient) GetHosts(domain string) (*namecheap.DomainsDNSGetHostsCommandResponse, error) {
	if f.getHosts != nil {
		return f.getHosts(domain)
	}
	return emptyHostsResponse(domain), nil
}

func (f *fakeDNSClient) SetHosts(args *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error) {
	if f.setHosts != nil {
		return f.setHosts(args)
	}
	domain := ""
	if args.Domain != nil {
		domain = *args.Domain
	}
	success := true
	domainStr := domain
	return &namecheap.DomainsDNSSetHostsCommandResponse{
		DomainDNSSetHostsResult: &namecheap.DomainDNSSetHostsResult{
			Domain:    &domainStr,
			IsSuccess: &success,
		},
	}, nil
}

// emptyHostsResponse returns a minimal GetHosts response with no records.
func emptyHostsResponse(domain string) *namecheap.DomainsDNSGetHostsCommandResponse {
	d := domain
	et := "NONE"
	usingOurDNS := true
	hosts := []namecheap.DomainsDNSHostRecordDetailed{}
	return &namecheap.DomainsDNSGetHostsCommandResponse{
		DomainDNSGetHostsResult: &namecheap.DomainDNSGetHostsResult{
			Domain:        &d,
			EmailType:     &et,
			IsUsingOurDNS: &usingOurDNS,
			Hosts:         &hosts,
		},
	}
}

// hostResponse builds a GetHosts response with the provided records.
func hostResponse(domain string, records []namecheap.DomainsDNSHostRecordDetailed) *namecheap.DomainsDNSGetHostsCommandResponse {
	resp := emptyHostsResponse(domain)
	resp.DomainDNSGetHostsResult.Hosts = &records
	return resp
}

func ptr[T any](v T) *T { return &v }

// ---- Type + basic driver construction ----

func TestDNSDriver_Type(t *testing.T) {
	d := NewDNSDriverWithClient(&fakeDNSClient{})
	if got := d.Type(); got != "infra.dns" {
		t.Errorf("Type() = %q, want infra.dns", got)
	}
}

func TestDNSDriver_ProviderIDFormat(t *testing.T) {
	d := NewDNSDriverWithClient(&fakeDNSClient{})
	if got := d.ProviderIDFormat(); got != interfaces.IDFormatDomainName {
		t.Errorf("ProviderIDFormat() = %v, want IDFormatDomainName", got)
	}
}

func TestDNSDriver_SensitiveKeys(t *testing.T) {
	d := NewDNSDriverWithClient(&fakeDNSClient{})
	if keys := d.SensitiveKeys(); len(keys) != 0 {
		t.Errorf("SensitiveKeys() = %v, want nil/empty", keys)
	}
}

// ---- Create ----

func TestDNSDriver_Create_CallsSetHosts(t *testing.T) {
	var called int
	var gotDomain string
	var gotCount int
	fake := &fakeDNSClient{
		setHosts: func(args *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error) {
			called++
			if args.Domain != nil {
				gotDomain = *args.Domain
			}
			if args.Records != nil {
				gotCount = len(*args.Records)
			}
			domain := gotDomain
			ok := true
			return &namecheap.DomainsDNSSetHostsCommandResponse{
				DomainDNSSetHostsResult: &namecheap.DomainDNSSetHostsResult{Domain: &domain, IsSuccess: &ok},
			}, nil
		},
	}
	d := NewDNSDriverWithClient(fake)
	spec := interfaces.ResourceSpec{
		Name: "example.com",
		Type: "infra.dns",
		Config: map[string]any{
			"domain": "example.com",
			"records": []any{
				map[string]any{"type": "A", "name": "@", "data": "1.2.3.4", "ttl": 1800},
			},
		},
	}
	out, err := d.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if called == 0 {
		t.Error("SetHosts was not called")
	}
	if gotDomain != "example.com" {
		t.Errorf("SetHosts domain = %q, want example.com", gotDomain)
	}
	if gotCount != 1 {
		t.Errorf("SetHosts record count = %d, want 1", gotCount)
	}
	if out == nil {
		t.Fatal("Create returned nil output")
	}
	if out.ProviderID != "example.com" {
		t.Errorf("ProviderID = %q, want example.com", out.ProviderID)
	}
}

func TestDNSDriver_Create_PropagatesSetHostsError(t *testing.T) {
	fake := &fakeDNSClient{
		setHosts: func(_ *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error) {
			return nil, errors.New("api error")
		},
	}
	d := NewDNSDriverWithClient(fake)
	spec := interfaces.ResourceSpec{
		Name:   "example.com",
		Type:   "infra.dns",
		Config: map[string]any{"domain": "example.com", "records": []any{}},
	}
	_, err := d.Create(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---- Read ----

func TestDNSDriver_Read_ReturnsOutput(t *testing.T) {
	fake := &fakeDNSClient{
		getHosts: func(domain string) (*namecheap.DomainsDNSGetHostsCommandResponse, error) {
			return hostResponse(domain, []namecheap.DomainsDNSHostRecordDetailed{
				{Name: ptr("@"), Type: ptr("A"), Address: ptr("1.2.3.4"), TTL: ptr(1800), MXPref: ptr(10), HostId: ptr(1), AssociatedAppTitle: ptr(""), FriendlyName: ptr(""), IsActive: ptr(true), IsDDNSEnabled: ptr(false)},
			}), nil
		},
	}
	d := NewDNSDriverWithClient(fake)
	out, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "example.com", Type: "infra.dns", ProviderID: "example.com"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.ProviderID != "example.com" {
		t.Errorf("ProviderID = %q, want example.com", out.ProviderID)
	}
	if cnt, _ := out.Outputs["record_count"].(int); cnt != 1 {
		t.Errorf("record_count = %v, want 1", out.Outputs["record_count"])
	}
	// Each record stored as record_0, record_1, ...
	rec0, ok := out.Outputs["record_0"].(map[string]any)
	if !ok {
		t.Fatalf("record_0 missing from outputs; got %+v", out.Outputs)
	}
	if rec0["type"] != "A" {
		t.Errorf("record_0.type = %v, want A", rec0["type"])
	}
}

func TestDNSDriver_Read_UsesNameWhenProviderIDEmpty(t *testing.T) {
	var gotDomain string
	fake := &fakeDNSClient{
		getHosts: func(domain string) (*namecheap.DomainsDNSGetHostsCommandResponse, error) {
			gotDomain = domain
			return emptyHostsResponse(domain), nil
		},
	}
	d := NewDNSDriverWithClient(fake)
	_, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "my-domain.com", Type: "infra.dns"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if gotDomain != "my-domain.com" {
		t.Errorf("GetHosts called with %q, want my-domain.com", gotDomain)
	}
}

func TestDNSDriver_Read_PropagatesGetHostsError(t *testing.T) {
	fake := &fakeDNSClient{
		getHosts: func(_ string) (*namecheap.DomainsDNSGetHostsCommandResponse, error) {
			return nil, errors.New("API unavailable")
		},
	}
	d := NewDNSDriverWithClient(fake)
	_, err := d.Read(context.Background(), interfaces.ResourceRef{Name: "x.com", Type: "infra.dns", ProviderID: "x.com"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---- Update ----

func TestDNSDriver_Update_CallsSetHosts(t *testing.T) {
	var calledSet int
	fake := &fakeDNSClient{
		setHosts: func(_ *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error) {
			calledSet++
			domain := "example.com"
			ok := true
			return &namecheap.DomainsDNSSetHostsCommandResponse{
				DomainDNSSetHostsResult: &namecheap.DomainDNSSetHostsResult{Domain: &domain, IsSuccess: &ok},
			}, nil
		},
	}
	d := NewDNSDriverWithClient(fake)
	ref := interfaces.ResourceRef{Name: "example.com", Type: "infra.dns", ProviderID: "example.com"}
	spec := interfaces.ResourceSpec{
		Name:   "example.com",
		Type:   "infra.dns",
		Config: map[string]any{"domain": "example.com", "records": []any{map[string]any{"type": "A", "name": "@", "data": "5.6.7.8", "ttl": 600}}},
	}
	out, err := d.Update(context.Background(), ref, spec)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if calledSet == 0 {
		t.Error("SetHosts not called")
	}
	if out == nil {
		t.Fatal("Update returned nil")
	}
}

// ---- Delete ----

func TestDNSDriver_Delete_ClearsRecords(t *testing.T) {
	var gotCount int
	fake := &fakeDNSClient{
		setHosts: func(args *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error) {
			if args.Records != nil {
				gotCount = len(*args.Records)
			}
			domain := "example.com"
			ok := true
			return &namecheap.DomainsDNSSetHostsCommandResponse{
				DomainDNSSetHostsResult: &namecheap.DomainDNSSetHostsResult{Domain: &domain, IsSuccess: &ok},
			}, nil
		},
	}
	d := NewDNSDriverWithClient(fake)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "example.com", Type: "infra.dns", ProviderID: "example.com"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if gotCount != 0 {
		t.Errorf("Delete sent %d records, want 0 (empty record set)", gotCount)
	}
}

func TestDNSDriver_Delete_PropagatesError(t *testing.T) {
	fake := &fakeDNSClient{
		setHosts: func(_ *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error) {
			return nil, errors.New("api error")
		},
	}
	d := NewDNSDriverWithClient(fake)
	err := d.Delete(context.Background(), interfaces.ResourceRef{Name: "x.com", Type: "infra.dns", ProviderID: "x.com"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---- Diff ----

func TestDNSDriver_Diff_NilCurrentNeedsUpdate(t *testing.T) {
	d := NewDNSDriverWithClient(&fakeDNSClient{})
	spec := interfaces.ResourceSpec{
		Name:   "example.com",
		Type:   "infra.dns",
		Config: map[string]any{"domain": "example.com", "records": []any{}},
	}
	res, err := d.Diff(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !res.NeedsUpdate {
		t.Error("NeedsUpdate should be true when current is nil")
	}
}

func TestDNSDriver_Diff_NoOp(t *testing.T) {
	d := NewDNSDriverWithClient(&fakeDNSClient{})
	spec := interfaces.ResourceSpec{
		Name: "example.com",
		Type: "infra.dns",
		Config: map[string]any{
			"domain": "example.com",
			"records": []any{
				map[string]any{"type": "A", "name": "@", "data": "1.2.3.4", "ttl": 1800},
			},
		},
	}
	// Build current output matching desired.
	current := &interfaces.ResourceOutput{
		Name:       "example.com",
		Type:       "infra.dns",
		ProviderID: "example.com",
		Outputs: map[string]any{
			"domain":       "example.com",
			"record_count": 1,
			"record_0": map[string]any{
				"type":    "A",
				"name":    "@",
				"address": "1.2.3.4",
				"ttl":     1800,
				"mx_pref": 0,
			},
		},
	}
	res, err := d.Diff(context.Background(), spec, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if res.NeedsUpdate {
		t.Errorf("NeedsUpdate should be false when current matches desired; changes: %v", res.Changes)
	}
}

func TestDNSDriver_Diff_DetectsDataChange(t *testing.T) {
	d := NewDNSDriverWithClient(&fakeDNSClient{})
	spec := interfaces.ResourceSpec{
		Name: "example.com",
		Type: "infra.dns",
		Config: map[string]any{
			"domain": "example.com",
			"records": []any{
				map[string]any{"type": "A", "name": "@", "data": "9.9.9.9", "ttl": 1800},
			},
		},
	}
	current := &interfaces.ResourceOutput{
		Outputs: map[string]any{
			"record_count": 1,
			"record_0": map[string]any{
				"type": "A", "name": "@", "address": "1.2.3.4", "ttl": 1800, "mx_pref": 0,
			},
		},
	}
	res, err := d.Diff(context.Background(), spec, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !res.NeedsUpdate {
		t.Error("NeedsUpdate should be true when record data changed")
	}
}

func TestDNSDriver_Diff_DetectsAddedRecord(t *testing.T) {
	d := NewDNSDriverWithClient(&fakeDNSClient{})
	spec := interfaces.ResourceSpec{
		Name: "example.com",
		Type: "infra.dns",
		Config: map[string]any{
			"domain": "example.com",
			"records": []any{
				map[string]any{"type": "A", "name": "@", "data": "1.2.3.4", "ttl": 1800},
				map[string]any{"type": "CNAME", "name": "www", "data": "example.com.", "ttl": 1800},
			},
		},
	}
	current := &interfaces.ResourceOutput{
		Outputs: map[string]any{
			"record_count": 1,
			"record_0": map[string]any{
				"type": "A", "name": "@", "address": "1.2.3.4", "ttl": 1800, "mx_pref": 0,
			},
		},
	}
	res, err := d.Diff(context.Background(), spec, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !res.NeedsUpdate {
		t.Error("NeedsUpdate should be true when a record was added")
	}
}

func TestDNSDriver_Diff_DetectsRemovedRecord(t *testing.T) {
	d := NewDNSDriverWithClient(&fakeDNSClient{})
	spec := interfaces.ResourceSpec{
		Name:   "example.com",
		Type:   "infra.dns",
		Config: map[string]any{"domain": "example.com", "records": []any{}},
	}
	current := &interfaces.ResourceOutput{
		Outputs: map[string]any{
			"record_count": 1,
			"record_0": map[string]any{
				"type": "A", "name": "@", "address": "1.2.3.4", "ttl": 1800, "mx_pref": 0,
			},
		},
	}
	res, err := d.Diff(context.Background(), spec, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !res.NeedsUpdate {
		t.Error("NeedsUpdate should be true when a record was removed")
	}
}

// ---- HealthCheck ----

func TestDNSDriver_HealthCheck_Healthy(t *testing.T) {
	d := NewDNSDriverWithClient(&fakeDNSClient{})
	res, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "x.com", Type: "infra.dns", ProviderID: "x.com"})
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if !res.Healthy {
		t.Error("expected healthy")
	}
}

func TestDNSDriver_HealthCheck_Error(t *testing.T) {
	fake := &fakeDNSClient{
		getHosts: func(_ string) (*namecheap.DomainsDNSGetHostsCommandResponse, error) {
			return nil, errors.New("API down")
		},
	}
	d := NewDNSDriverWithClient(fake)
	res, err := d.HealthCheck(context.Background(), interfaces.ResourceRef{Name: "x.com", Type: "infra.dns", ProviderID: "x.com"})
	if err != nil {
		t.Fatalf("HealthCheck returned unexpected error: %v", err)
	}
	if res.Healthy {
		t.Error("expected unhealthy when GetHosts errors")
	}
}

// ---- parseDNSSpec (internal logic) ----

func TestParseDNSSpec_MissingDomainFallsBackToName(t *testing.T) {
	spec := interfaces.ResourceSpec{
		Name:   "fallback.com",
		Type:   "infra.dns",
		Config: map[string]any{"records": []any{}},
	}
	domain, records, err := parseDNSSpec(spec)
	if err != nil {
		t.Fatalf("parseDNSSpec: %v", err)
	}
	if domain != "fallback.com" {
		t.Errorf("domain = %q, want fallback.com", domain)
	}
	if len(records) != 0 {
		t.Errorf("records = %v, want empty", records)
	}
}

func TestParseDNSSpec_InvalidRecordMap(t *testing.T) {
	spec := interfaces.ResourceSpec{
		Name: "x.com",
		Type: "infra.dns",
		Config: map[string]any{
			"domain":  "x.com",
			"records": []any{"not-a-map"},
		},
	}
	_, _, err := parseDNSSpec(spec)
	if err == nil {
		t.Fatal("expected error for non-map record entry")
	}
}

func TestParseDNSSpec_MissingRecordType(t *testing.T) {
	spec := interfaces.ResourceSpec{
		Name: "x.com",
		Type: "infra.dns",
		Config: map[string]any{
			"domain": "x.com",
			"records": []any{
				map[string]any{"name": "@", "data": "1.2.3.4"},
			},
		},
	}
	_, _, err := parseDNSSpec(spec)
	if err == nil {
		t.Fatal("expected error for missing record type")
	}
}

func TestParseDNSSpec_TTLClampedToMin(t *testing.T) {
	spec := interfaces.ResourceSpec{
		Name: "x.com",
		Type: "infra.dns",
		Config: map[string]any{
			"domain": "x.com",
			"records": []any{
				map[string]any{"type": "A", "name": "@", "data": "1.1.1.1", "ttl": 10},
			},
		},
	}
	_, records, err := parseDNSSpec(spec)
	if err != nil {
		t.Fatalf("parseDNSSpec: %v", err)
	}
	if records[0].TTL != 60 {
		t.Errorf("TTL = %d, want 60 (clamped from 10)", records[0].TTL)
	}
}

// ---- diffRecords ----

func TestDiffRecords_BothEmpty(t *testing.T) {
	changes := diffRecords(nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for empty sets; got %d", len(changes))
	}
}

func TestDiffRecords_SameRecords(t *testing.T) {
	r := []dnsRecord{{Type: "A", Name: "@", Data: "1.2.3.4", TTL: 1800}}
	changes := diffRecords(r, r)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for identical sets; got %v", changes)
	}
}

func TestDiffRecords_Create(t *testing.T) {
	desired := []dnsRecord{{Type: "A", Name: "@", Data: "1.2.3.4", TTL: 1800}}
	changes := diffRecords(nil, desired)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change (create); got %d", len(changes))
	}
	if changes[0].Old != nil {
		t.Error("Old should be nil for create")
	}
}

func TestDiffRecords_Delete(t *testing.T) {
	current := []dnsRecord{{Type: "A", Name: "@", Data: "1.2.3.4", TTL: 1800}}
	changes := diffRecords(current, nil)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change (delete); got %d", len(changes))
	}
	if changes[0].New != nil {
		t.Error("New should be nil for delete")
	}
}

func TestDiffRecords_Update(t *testing.T) {
	current := []dnsRecord{{Type: "A", Name: "@", Data: "1.2.3.4", TTL: 1800}}
	desired := []dnsRecord{{Type: "A", Name: "@", Data: "5.6.7.8", TTL: 1800}}
	changes := diffRecords(current, desired)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change (update); got %d", len(changes))
	}
}

func TestDiffRecords_DeterministicOrder(t *testing.T) {
	current := []dnsRecord{
		{Type: "A", Name: "b", Data: "2.2.2.2", TTL: 1800},
		{Type: "A", Name: "a", Data: "1.1.1.1", TTL: 1800},
	}
	desired := []dnsRecord{
		{Type: "A", Name: "c", Data: "3.3.3.3", TTL: 1800},
	}
	c1 := diffRecords(current, desired)
	c2 := diffRecords(current, desired)
	if len(c1) != len(c2) {
		t.Fatalf("non-deterministic length: %d vs %d", len(c1), len(c2))
	}
	for i := range c1 {
		if c1[i].Path != c2[i].Path {
			t.Errorf("index %d: c1.Path=%q c2.Path=%q", i, c1[i].Path, c2[i].Path)
		}
	}
}
