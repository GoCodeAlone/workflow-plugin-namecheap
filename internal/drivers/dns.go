// Package drivers — Namecheap DNS ResourceDriver.
//
// One IaC resource == one Namecheap domain's full record set.
// ProviderID is the domain apex (e.g. "example.com").
//
// Namecheap's API requires the full record list on every SetHosts call
// (no per-record create/delete endpoints). The IaC pipeline calls the
// driver's Diff() method first to compute what (if anything) needs to
// change; when Diff reports NeedsUpdate, the engine invokes
// Create/Update which writes the desired record list as a single
// SetHosts call, replacing the zone wholesale. Diff sees the current
// records via Read (GetHosts); Create/Update themselves do NOT
// re-read or merge — they trust the engine's plan.
package drivers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/GoCodeAlone/workflow/interfaces"
	"github.com/namecheap/go-namecheap-sdk/v2/namecheap"
)

// DNSClient is the subset of the Namecheap DomainsDNSService used by
// DNSDriver, declared as an interface for test injection.
type DNSClient interface {
	GetHosts(domain string) (*namecheap.DomainsDNSGetHostsCommandResponse, error)
	SetHosts(args *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error)
}

// realDNSClient wraps *namecheap.DomainsDNSService to satisfy DNSClient.
type realDNSClient struct{ svc *namecheap.DomainsDNSService }

func (r *realDNSClient) GetHosts(domain string) (*namecheap.DomainsDNSGetHostsCommandResponse, error) {
	return r.svc.GetHosts(domain)
}

func (r *realDNSClient) SetHosts(args *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error) {
	return r.svc.SetHosts(args)
}

// DNSDriver implements interfaces.ResourceDriver for infra.dns backed
// by the Namecheap API. One resource covers the full record set for
// one domain.
type DNSDriver struct {
	client DNSClient
}

// NewDNSDriver returns a DNSDriver backed by a real Namecheap client.
func NewDNSDriver(c *namecheap.Client) *DNSDriver {
	return &DNSDriver{client: &realDNSClient{svc: c.DomainsDNS}}
}

// NewDNSDriverWithClient returns a driver with an injected test client.
func NewDNSDriverWithClient(c DNSClient) *DNSDriver {
	return &DNSDriver{client: c}
}

// ---- interfaces.ResourceDriver ----

// Create applies the desired record set to the domain. It is idempotent:
// Create writes the desired record set as a single SetHosts call,
// replacing the existing zone wholesale. It does not pre-diff or
// short-circuit on no-change inputs — the engine's Plan→Diff→Apply
// pipeline ensures Create only fires when work is needed. Callers
// that invoke Create directly outside that pipeline (rare) will
// always incur a SetHosts call.
func (d *DNSDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dns create %q: %w", spec.Name, err)
	}
	domain, records, err := parseDNSSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("dns create %q: %w", spec.Name, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dns create %q: %w", spec.Name, err)
	}
	if err := d.setHosts(domain, records); err != nil {
		return nil, fmt.Errorf("dns create %q: %w", spec.Name, err)
	}
	// Return outputs from the just-written desired set instead of a
	// follow-up GetHosts call. The Namecheap setHosts call is
	// authoritative — if it succeeded, the zone now matches `records`.
	// Skipping the read halves the API call count per apply (relevant
	// against the 20 req/min per-IP cap).
	return dnsOutputFromRecords(spec.Name, domain, records), nil
}

// Read fetches the current record set for the domain.
func (d *DNSDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dns read %q: %w", ref.Name, err)
	}
	domain := ref.ProviderID
	if domain == "" {
		domain = ref.Name
	}
	resp, err := d.client.GetHosts(domain)
	if err != nil {
		return nil, fmt.Errorf("dns read %q: %w", ref.Name, err)
	}
	return dnsOutput(ref.Name, domain, resp), nil
}

// Update replaces the full record set with the desired spec.
func (d *DNSDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dns update %q: %w", ref.Name, err)
	}
	currentDomain := ref.ProviderID
	if currentDomain == "" {
		currentDomain = ref.Name
	}
	specDomain, records, err := parseDNSSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("dns update %q: %w", ref.Name, err)
	}
	// Reject in-place domain renames. A change of zone is an identity
	// change, not an update — Diff/Plan should have flagged this as a
	// replace (NeedsReplace=true) and the engine should have routed
	// Delete-then-Create. If we silently rewrote `currentDomain` with
	// `specDomain`'s records, the old zone would be left untouched and
	// the new one stomped over.
	if !strings.EqualFold(specDomain, currentDomain) {
		return nil, fmt.Errorf("dns update %q: spec.domain %q does not match current %q — domain change requires resource replace, not update", ref.Name, specDomain, currentDomain)
	}
	// Re-check ctx before the blocking API call so cancellations
	// during config-parse don't waste an API call.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dns update %q: %w", ref.Name, err)
	}
	if err := d.setHosts(currentDomain, records); err != nil {
		return nil, fmt.Errorf("dns update %q: %w", ref.Name, err)
	}
	// Skip the follow-up GetHosts; the desired set is authoritative
	// after setHosts succeeds. See Create's rationale.
	return dnsOutputFromRecords(ref.Name, currentDomain, records), nil
}

// Delete clears all non-default records from the domain (sets an empty
// record set). Namecheap does not delete the domain itself.
func (d *DNSDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("dns delete %q: %w", ref.Name, err)
	}
	domain := ref.ProviderID
	if domain == "" {
		domain = ref.Name
	}
	// Set an empty record list to clear user-managed records.
	domainStr := domain
	emptyRecords := []namecheap.DomainsDNSHostRecord{}
	_, err := d.client.SetHosts(&namecheap.DomainsDNSSetHostsArgs{
		Domain:  &domainStr,
		Records: &emptyRecords,
	})
	if err != nil {
		return fmt.Errorf("dns delete %q: %w", ref.Name, err)
	}
	return nil
}

// Diff compares desired spec against current output and returns whether
// an update is needed. A change of the desired domain (spec.domain or
// spec.Name) away from the current ProviderID is a resource-identity
// change, not an update — flagged as NeedsReplace so the engine routes
// Delete-then-Create instead of Update against the wrong zone.
func (d *DNSDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	desiredDomain, desiredRecords, err := parseDNSSpec(desired)
	if err != nil {
		return nil, fmt.Errorf("dns diff: parse desired: %w", err)
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
	currentRecords := recordsFromOutputs(current.Outputs)
	changes := diffRecords(currentRecords, desiredRecords)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

// HealthCheck probes connectivity to the domain by fetching its hosts.
func (d *DNSDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	if err := ctx.Err(); err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	domain := ref.ProviderID
	if domain == "" {
		domain = ref.Name
	}
	_, err := d.client.GetHosts(domain)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	return &interfaces.HealthResult{Healthy: true, Message: "ok"}, nil
}

// Scale is a no-op for DNS; DNS does not have a replica count.
func (d *DNSDriver) Scale(ctx context.Context, ref interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return d.Read(ctx, ref)
}

// Type returns the IaC resource type this driver handles.
func (d *DNSDriver) Type() string { return "infra.dns" }

// SensitiveKeys returns nil; DNS records are not sensitive.
func (d *DNSDriver) SensitiveKeys() []string { return nil }

// ProviderIDFormat declares that ProviderIDs are domain names.
func (d *DNSDriver) ProviderIDFormat() interfaces.ProviderIDFormat {
	return interfaces.IDFormatDomainName
}

// ---- internal helpers ----

// dnsRecord is the canonical internal representation of a single DNS record.
type dnsRecord struct {
	Type string
	Name string
	Data string
	TTL  int
	MX   int
}

// parseDNSSpec extracts the domain + records from a ResourceSpec.Config.
// Config keys: domain (string), records ([]any of map[string]any).
func parseDNSSpec(spec interfaces.ResourceSpec) (string, []dnsRecord, error) {
	domain, _ := spec.Config["domain"].(string)
	if domain == "" {
		// Fall back to the resource name if domain is not specified.
		domain = spec.Name
	}
	if domain == "" {
		return "", nil, fmt.Errorf("dns: config missing required key 'domain'")
	}

	// records is REQUIRED — a missing key OR a wrongly-typed value
	// must error out. Silently coercing to an empty slice would let
	// SetHosts wipe the zone on the next apply (Namecheap setHosts
	// is a whole-zone replace). An explicitly-empty `records: []`
	// is allowed and DOES intentionally drop every record; only the
	// missing-key / wrong-type case is rejected here.
	rawRecordsAny, hasRecords := spec.Config["records"]
	if !hasRecords {
		return "", nil, fmt.Errorf("dns: config missing required key 'records' (use an explicit 'records: []' to drop every record)")
	}
	rawRecords, ok := rawRecordsAny.([]any)
	if !ok {
		return "", nil, fmt.Errorf("dns: config 'records' must be an array, got %T", rawRecordsAny)
	}
	records := make([]dnsRecord, 0, len(rawRecords))
	for i, r := range rawRecords {
		m, ok := r.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("dns: records[%d] is not a map", i)
		}
		rec, err := parseRecordMap(m, i)
		if err != nil {
			return "", nil, err
		}
		records = append(records, rec)
	}
	return domain, records, nil
}

func parseRecordMap(m map[string]any, idx int) (dnsRecord, error) {
	rtype, _ := m["type"].(string)
	if rtype == "" {
		return dnsRecord{}, fmt.Errorf("dns: records[%d].type is required", idx)
	}
	name, _ := m["name"].(string)
	if name == "" {
		return dnsRecord{}, fmt.Errorf("dns: records[%d].name is required", idx)
	}
	data, _ := m["data"].(string)
	if data == "" {
		return dnsRecord{}, fmt.Errorf("dns: records[%d].data is required", idx)
	}
	ttl := 1800
	switch v := m["ttl"].(type) {
	case int:
		ttl = v
	case float64:
		ttl = int(v)
	}
	if ttl < 60 {
		ttl = 60
	}
	mx := 0
	switch v := m["mx"].(type) {
	case int:
		mx = v
	case float64:
		mx = int(v)
	}
	return dnsRecord{Type: strings.ToUpper(rtype), Name: name, Data: data, TTL: ttl, MX: mx}, nil
}

// setHosts writes the full record list to Namecheap.
func (d *DNSDriver) setHosts(domain string, records []dnsRecord) error {
	ncRecords := make([]namecheap.DomainsDNSHostRecord, 0, len(records))

	// Determine EmailType from MX records.
	var emailType *string
	hasMX := false
	for _, r := range records {
		if r.Type == "MX" {
			hasMX = true
			break
		}
	}
	if hasMX {
		et := "MX"
		emailType = &et
	}

	for _, r := range records {
		rec := r // local copy
		hostName := rec.Name
		recType := rec.Type
		address := rec.Data
		ttl := rec.TTL
		ncRec := namecheap.DomainsDNSHostRecord{
			HostName:   &hostName,
			RecordType: &recType,
			Address:    &address,
			TTL:        &ttl,
		}
		if rec.Type == "MX" {
			if rec.MX < 0 || rec.MX > 255 {
				return fmt.Errorf("namecheap: MX record %q priority %d out of range [0,255]", rec.Name, rec.MX)
			}
			pref := uint8(rec.MX) //nolint:gosec // bounds-checked above
			ncRec.MXPref = &pref
		}
		ncRecords = append(ncRecords, ncRec)
	}

	domainStr := domain
	_, err := d.client.SetHosts(&namecheap.DomainsDNSSetHostsArgs{
		Domain:    &domainStr,
		Records:   &ncRecords,
		EmailType: emailType,
	})
	return err
}

// dnsOutput converts a GetHosts API response into ResourceOutput.
//
// Outputs are stored as a flat map[string]any with primitive
// string/int leaves only — no typed slices, no nested non-map
// values. This shape survives both serialization paths the plugin
// might encounter: the iacserver's JSON-bytes contract
// (OutputsJson) AND structpb (used by some host gRPC surfaces).
// Each record is stored under a numbered key (record_0, record_1,
// …) rather than a `records: []map{...}` slice because some hosts
// reject heterogeneous slices through structpb.
// dnsOutputFromRecords produces a ResourceOutput from an in-memory
// []dnsRecord without an upstream API call. Used by Create/Update to
// return outputs reflecting the freshly-written desired state,
// avoiding the read-after-write round trip against Namecheap's 20
// req/min per-IP cap. EmailType is omitted (we don't know what
// Namecheap will report) and can be re-read out-of-band via Read.
func dnsOutputFromRecords(name, domain string, records []dnsRecord) *interfaces.ResourceOutput {
	outputs := map[string]any{
		"domain":       domain,
		"record_count": len(records),
	}
	for i, r := range records {
		rec := map[string]any{
			"name":    r.Name,
			"type":    r.Type,
			"address": r.Data,
			"ttl":     r.TTL,
		}
		if r.Type == "MX" {
			rec["mx_pref"] = r.MX
		}
		outputs[fmt.Sprintf("record_%d", i)] = rec
	}
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.dns",
		ProviderID: domain,
		Outputs:    outputs,
		Status:     "active",
	}
}

func dnsOutput(name, domain string, resp *namecheap.DomainsDNSGetHostsCommandResponse) *interfaces.ResourceOutput {
	outputs := map[string]any{
		"domain":       domain,
		"record_count": 0,
	}
	if resp != nil && resp.DomainDNSGetHostsResult != nil {
		result := resp.DomainDNSGetHostsResult
		if result.Hosts != nil {
			outputs["record_count"] = len(*result.Hosts)
			// Store each record as a flat keyed entry so the outputs map
			// contains only map[string]any at the top level (structpb-safe).
			for i, h := range *result.Hosts {
				key := fmt.Sprintf("record_%d", i)
				rec := map[string]any{}
				if h.Name != nil {
					rec["name"] = *h.Name
				}
				if h.Type != nil {
					rec["type"] = *h.Type
				}
				if h.Address != nil {
					rec["address"] = *h.Address
				}
				if h.TTL != nil {
					rec["ttl"] = *h.TTL
				}
				if h.MXPref != nil {
					rec["mx_pref"] = *h.MXPref
				}
				outputs[key] = rec
			}
		}
		if result.EmailType != nil {
			outputs["email_type"] = *result.EmailType
		}
	}
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.dns",
		ProviderID: domain,
		Outputs:    outputs,
		Status:     "active",
	}
}

// recordsFromOutputs reconstructs a []dnsRecord from ResourceOutput.Outputs.
// Used by Diff to compare current state against desired without a live API call.
//
// record_count may arrive as int (in-process, from dnsOutput) or as float64
// (after a JSON marshal/unmarshal round-trip via gRPC structpb). Accept both.
func recordsFromOutputs(outputs map[string]any) []dnsRecord {
	var count int
	switch v := outputs["record_count"].(type) {
	case int:
		count = v
	case float64:
		count = int(v)
	}
	records := make([]dnsRecord, 0, count)
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("record_%d", i)
		recAny, ok := outputs[key]
		if !ok {
			continue
		}
		recMap, ok := recAny.(map[string]any)
		if !ok {
			continue
		}
		var r dnsRecord
		r.Name, _ = recMap["name"].(string)
		r.Type, _ = recMap["type"].(string)
		r.Data, _ = recMap["address"].(string)
		switch v := recMap["ttl"].(type) {
		case int:
			r.TTL = v
		case float64:
			r.TTL = int(v)
		}
		switch v := recMap["mx_pref"].(type) {
		case int:
			r.MX = v
		case float64:
			r.MX = int(v)
		}
		records = append(records, r)
	}
	return records
}

// recordKey returns a canonical string key for a DNS record. Includes
// Data so duplicate (Type, Name) pairs with distinct values (e.g.,
// multiple A/AAAA/TXT records on the same host) are NOT collapsed.
// TTL/MX are intentionally excluded so a TTL-only or priority-only
// change is detected as a change-of-existing rather than an
// add-and-remove pair.
func recordKey(r dnsRecord) string {
	return fmt.Sprintf("%s/%s/%s", strings.ToUpper(r.Type), strings.ToLower(r.Name), r.Data)
}

// diffRecords returns the FieldChange slice describing differences between
// current and desired record sets. Each changed/added/removed record produces
// one FieldChange entry.
//
// Records sharing the same (Type, Name) but distinct Data are treated as
// independent records (e.g., two A records at the apex pointing at
// different IPs). A change to one does not perturb the other.
func diffRecords(current, desired []dnsRecord) []interfaces.FieldChange {
	cur := make(map[string]dnsRecord, len(current))
	for _, r := range current {
		cur[recordKey(r)] = r
	}
	des := make(map[string]dnsRecord, len(desired))
	for _, r := range desired {
		des[recordKey(r)] = r
	}

	var changes []interfaces.FieldChange

	// Added or changed records.
	for k, d := range des {
		c, exists := cur[k]
		if !exists {
			changes = append(changes, interfaces.FieldChange{
				Path: "record/" + k,
				Old:  nil,
				New:  recordToMap(d),
			})
			continue
		}
		// Same (Type, Name, Data) — only TTL/MX can still differ.
		if c.TTL != d.TTL || c.MX != d.MX {
			changes = append(changes, interfaces.FieldChange{
				Path: "record/" + k,
				Old:  recordToMap(c),
				New:  recordToMap(d),
			})
		}
	}

	// Removed records.
	for k, c := range cur {
		if _, ok := des[k]; !ok {
			changes = append(changes, interfaces.FieldChange{
				Path: "record/" + k,
				Old:  recordToMap(c),
				New:  nil,
			})
		}
	}

	// Sort for determinism.
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})
	return changes
}

// recordToMap converts a dnsRecord to map[string]any (structpb-safe)
// for inclusion in DiffResult.Changes. Field keys mirror the user-
// facing config schema so plan output reads cleanly (`mx` not
// `mx_pref`, `data` not `address`). The `mx` field is only included
// for MX records since priority is meaningless for other types.
func recordToMap(r dnsRecord) map[string]any {
	m := map[string]any{
		"type": r.Type,
		"name": r.Name,
		"data": r.Data,
		"ttl":  r.TTL,
	}
	if r.Type == "MX" {
		m["mx"] = r.MX
	}
	return m
}
