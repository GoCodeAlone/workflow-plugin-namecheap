// Package internal — typed pb.IaCProvider*Server for workflow-plugin-namecheap.
//
// Implements the minimum required typed IaC gRPC surface for infra.dns:
//   - pb.IaCProviderRequiredServer (Name, Version, Capabilities, Initialize,
//     Plan, Destroy, Status, Import, ResolveSizing, BootstrapStateBackend).
//   - pb.IaCProviderFinalizerServer (FinalizeApply — no-op for DNS).
//
// ComputePlanVersion is "v2" per the force-cutover mandate (ADR 0024 /
// workflow#699). Plugins that do not declare v2 are permanently rejected
// by wfctl v0.54.0+.
//
// Config crosses the gRPC boundary as JSON bytes (config_json field).
// No structpb.Struct, no Any.UnmarshalTo on the wire — per the strict-
// contracts hard invariant documented in workflow-plugin-digitalocean's
// iacserver.go.
package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-namecheap/internal/drivers"
	"github.com/GoCodeAlone/workflow/interfaces"
	"github.com/GoCodeAlone/workflow/platform"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	"github.com/namecheap/go-namecheap-sdk/v2/namecheap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Version is stamped at build time via -ldflags.
var Version = "dev"

// ncIaCServer satisfies pb.IaCProviderRequiredServer +
// pb.IaCProviderFinalizerServer for the Namecheap provider.
type ncIaCServer struct {
	pb.UnimplementedIaCProviderRequiredServer
	pb.UnimplementedIaCProviderFinalizerServer

	// drivers are populated by Initialize.
	dnsDriver      *drivers.DNSDriver
	transferDriver *drivers.TransferDriver
	// cfg is the last-applied provider config.
	cfg Config
}

// Compile-time interface assertions.
var (
	_ pb.IaCProviderRequiredServer  = (*ncIaCServer)(nil)
	_ pb.IaCProviderFinalizerServer = (*ncIaCServer)(nil)
)

// NewIaCServer constructs an uninitialised ncIaCServer ready for
// registration via sdk.ServeIaCPlugin.
func NewIaCServer() *ncIaCServer {
	return &ncIaCServer{}
}

// ---- Required service methods ----

func (s *ncIaCServer) Name(_ context.Context, _ *pb.NameRequest) (*pb.NameResponse, error) {
	return &pb.NameResponse{Name: "namecheap"}, nil
}

func (s *ncIaCServer) Version(_ context.Context, _ *pb.VersionRequest) (*pb.VersionResponse, error) {
	return &pb.VersionResponse{Version: Version}, nil
}

func (s *ncIaCServer) Capabilities(_ context.Context, _ *pb.CapabilitiesRequest) (*pb.CapabilitiesResponse, error) {
	return &pb.CapabilitiesResponse{
		Capabilities: []*pb.IaCCapabilityDeclaration{
			{
				ResourceType: "infra.dns",
				Tier:         1,
				Operations:   []string{"create", "read", "update", "delete"},
			},
			{
				ResourceType: "infra.domain_transfer",
				Tier:         1,
				Operations:   []string{"create", "read"},
			},
		},
		ComputePlanVersion: "v2",
	}, nil
}

// Initialize parses config_json and constructs the Namecheap client +
// DNSDriver. Returns ErrAuthMissing (wrapped) if required keys are absent.
func (s *ncIaCServer) Initialize(_ context.Context, req *pb.InitializeRequest) (*pb.InitializeResponse, error) {
	m, err := unmarshalJSONMapNC(req.GetConfigJson())
	if err != nil {
		return nil, fmt.Errorf("namecheap iacserver: parse config_json: %w", err)
	}
	cfg := Config{
		APIUser:  strVal(m, "api_user"),
		APIKey:   strVal(m, "api_key"),
		ClientIP: strVal(m, "client_ip"),
	}
	if b, ok := m["sandbox"].(bool); ok {
		cfg.Sandbox = b
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("namecheap iacserver: invalid config: %w", err)
	}
	s.cfg = cfg
	client := namecheap.NewClient(&namecheap.ClientOptions{
		UserName:   cfg.APIUser,
		ApiUser:    cfg.APIUser,
		ApiKey:     cfg.APIKey,
		ClientIp:   cfg.ClientIP,
		UseSandbox: cfg.Sandbox,
	})
	s.dnsDriver = drivers.NewDNSDriver(client)
	s.transferDriver = drivers.NewTransferDriver(client)
	return &pb.InitializeResponse{}, nil
}

// Plan computes the desired → current diff via platform.ComputePlan.
func (s *ncIaCServer) Plan(ctx context.Context, req *pb.PlanRequest) (*pb.PlanResponse, error) {
	if s.dnsDriver == nil || s.transferDriver == nil {
		return nil, fmt.Errorf("namecheap iacserver: Plan called before Initialize")
	}
	desired, err := specsFromPBNC(req.GetDesired())
	if err != nil {
		return nil, fmt.Errorf("namecheap iacserver: decode Plan desired: %w", err)
	}
	current, err := statesFromPBNC(req.GetCurrent())
	if err != nil {
		return nil, fmt.Errorf("namecheap iacserver: decode Plan current: %w", err)
	}
	p := &ncProvider{dnsDriver: s.dnsDriver, transferDriver: s.transferDriver}
	plan, err := platform.ComputePlan(ctx, p, desired, current)
	if err != nil {
		return nil, err
	}
	pbPlan, err := planToPBNC(&plan)
	if err != nil {
		return nil, fmt.Errorf("namecheap iacserver: encode Plan response: %w", err)
	}
	return &pb.PlanResponse{Plan: pbPlan}, nil
}

// Destroy deletes every listed resource by clearing its DNS records.
func (s *ncIaCServer) Destroy(ctx context.Context, req *pb.DestroyRequest) (*pb.DestroyResponse, error) {
	if s.dnsDriver == nil || s.transferDriver == nil {
		return nil, fmt.Errorf("namecheap iacserver: Destroy called before Initialize")
	}
	refs := refsFromPBNC(req.GetRefs())
	var destroyed []string
	var errs []*pb.ActionError
	for _, ref := range refs {
		driver, err := s.resourceDriver(ref.Type)
		if err == nil {
			err = driver.Delete(ctx, ref)
		}
		if err != nil {
			errs = append(errs, &pb.ActionError{Resource: ref.Name, Action: "delete", Error: err.Error()})
		} else {
			destroyed = append(destroyed, ref.Name)
		}
	}
	return &pb.DestroyResponse{
		Result: &pb.DestroyResult{
			Destroyed: destroyed,
			Errors:    errs,
		},
	}, nil
}

// Status returns the live status of the requested resources.
func (s *ncIaCServer) Status(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	if s.dnsDriver == nil || s.transferDriver == nil {
		return nil, fmt.Errorf("namecheap iacserver: Status called before Initialize")
	}
	refs := refsFromPBNC(req.GetRefs())
	statuses := make([]*pb.ResourceStatus, 0, len(refs))
	for _, ref := range refs {
		driver, err := s.resourceDriver(ref.Type)
		if err != nil {
			statuses = append(statuses, &pb.ResourceStatus{
				Name:   ref.Name,
				Type:   ref.Type,
				Status: "error",
			})
			continue
		}
		out, err := driver.Read(ctx, ref)
		if err != nil {
			statuses = append(statuses, &pb.ResourceStatus{
				Name:   ref.Name,
				Type:   ref.Type,
				Status: "error",
			})
			continue
		}
		outputsJSON, marshalErr := json.Marshal(out.Outputs)
		if marshalErr != nil {
			// A non-marshalable Outputs value would be a programmer bug
			// in the driver, not an upstream-API failure. Surface it as
			// the resource's error status with an empty OutputsJson so
			// the client sees something concrete rather than a silently
			// empty payload.
			statuses = append(statuses, &pb.ResourceStatus{
				Name:       out.Name,
				Type:       out.Type,
				ProviderId: out.ProviderID,
				Status:     "error",
			})
			continue
		}
		statuses = append(statuses, &pb.ResourceStatus{
			Name:        out.Name,
			Type:        out.Type,
			ProviderId:  out.ProviderID,
			Status:      out.Status,
			OutputsJson: outputsJSON,
		})
	}
	return &pb.StatusResponse{Statuses: statuses}, nil
}

// Import imports a domain's DNS state into IaC state.
func (s *ncIaCServer) Import(ctx context.Context, req *pb.ImportRequest) (*pb.ImportResponse, error) {
	if s.dnsDriver == nil || s.transferDriver == nil {
		return nil, fmt.Errorf("namecheap iacserver: Import called before Initialize")
	}
	resourceType := req.GetResourceType()
	if resourceType == "" {
		resourceType = "infra.dns"
	}
	ref := interfaces.ResourceRef{
		Name:       req.GetProviderId(),
		Type:       resourceType,
		ProviderID: req.GetProviderId(),
	}
	driver, err := s.resourceDriver(resourceType)
	if err != nil {
		return nil, err
	}
	out, err := driver.Read(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("namecheap iacserver: import %q: %w", req.GetProviderId(), err)
	}
	outputsJSON, err := json.Marshal(out.Outputs)
	if err != nil {
		return nil, fmt.Errorf("namecheap iacserver: marshal import outputs: %w", err)
	}
	now := time.Now()
	return &pb.ImportResponse{
		State: &pb.ResourceState{
			Id:          out.ProviderID,
			Name:        out.Name,
			Type:        out.Type,
			Provider:    "namecheap",
			ProviderId:  out.ProviderID,
			OutputsJson: outputsJSON,
			CreatedAt:   timestamppb.New(now),
			UpdatedAt:   timestamppb.New(now),
		},
	}, nil
}

func (s *ncIaCServer) resourceDriver(resourceType string) (interfaces.ResourceDriver, error) {
	switch resourceType {
	case "", "infra.dns":
		return s.dnsDriver, nil
	case "infra.domain_transfer":
		return s.transferDriver, nil
	default:
		return nil, fmt.Errorf("namecheap: unsupported resource type %q", resourceType)
	}
}

// ResolveSizing is not meaningful for DNS; returns nil sizing.
func (s *ncIaCServer) ResolveSizing(_ context.Context, _ *pb.ResolveSizingRequest) (*pb.ResolveSizingResponse, error) {
	return &pb.ResolveSizingResponse{Sizing: nil}, nil
}

// BootstrapStateBackend is not needed for DNS; returns nil result.
func (s *ncIaCServer) BootstrapStateBackend(_ context.Context, _ *pb.BootstrapStateBackendRequest) (*pb.BootstrapStateBackendResponse, error) {
	return &pb.BootstrapStateBackendResponse{Result: nil}, nil
}

// FinalizeApply is a no-op for DNS (no deferred updates).
func (s *ncIaCServer) FinalizeApply(_ context.Context, _ *pb.FinalizeApplyRequest) (*pb.FinalizeApplyResponse, error) {
	return &pb.FinalizeApplyResponse{}, nil
}

// ---- ncProvider bridges ncIaCServer → interfaces.IaCProvider for platform.ComputePlan ----

// ncProvider satisfies interfaces.IaCProvider using Namecheap resource drivers.
type ncProvider struct {
	dnsDriver      *drivers.DNSDriver
	transferDriver *drivers.TransferDriver
}

func (p *ncProvider) Name() string    { return "namecheap" }
func (p *ncProvider) Version() string { return Version }

func (p *ncProvider) Initialize(_ context.Context, _ map[string]any) error {
	// Already initialized in ncIaCServer.Initialize.
	return nil
}

func (p *ncProvider) Capabilities() []interfaces.IaCCapabilityDeclaration {
	return []interfaces.IaCCapabilityDeclaration{
		{ResourceType: "infra.dns", Tier: 1, Operations: []string{"create", "read", "update", "delete"}},
		{ResourceType: "infra.domain_transfer", Tier: 1, Operations: []string{"create", "read"}},
	}
}

func (p *ncProvider) ResourceDriver(resourceType string) (interfaces.ResourceDriver, error) {
	switch resourceType {
	case "", "infra.dns":
		return p.dnsDriver, nil
	case "infra.domain_transfer":
		return p.transferDriver, nil
	default:
		return nil, fmt.Errorf("namecheap: unsupported resource type %q", resourceType)
	}
}

func (p *ncProvider) Plan(ctx context.Context, desired []interfaces.ResourceSpec, current []interfaces.ResourceState) (*interfaces.IaCPlan, error) {
	plan, err := platform.ComputePlan(ctx, p, desired, current)
	return &plan, err
}

func (p *ncProvider) Destroy(_ context.Context, _ []interfaces.ResourceRef) (*interfaces.DestroyResult, error) {
	return &interfaces.DestroyResult{}, nil
}

func (p *ncProvider) Status(_ context.Context, _ []interfaces.ResourceRef) ([]interfaces.ResourceStatus, error) {
	return nil, nil
}

func (p *ncProvider) DetectDrift(_ context.Context, _ []interfaces.ResourceRef) ([]interfaces.DriftResult, error) {
	return nil, nil
}

func (p *ncProvider) Import(ctx context.Context, cloudID string, resourceType string) (*interfaces.ResourceState, error) {
	if resourceType == "" {
		resourceType = "infra.dns"
	}
	d, err := p.ResourceDriver(resourceType)
	if err != nil {
		return nil, err
	}
	out, err := d.Read(ctx, interfaces.ResourceRef{Name: cloudID, Type: resourceType, ProviderID: cloudID})
	if err != nil {
		return nil, fmt.Errorf("namecheap import: %w", err)
	}
	now := time.Now()
	return &interfaces.ResourceState{
		ID:         cloudID,
		Name:       out.Name,
		Type:       out.Type,
		Provider:   "namecheap",
		ProviderID: out.ProviderID,
		Outputs:    out.Outputs,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func (p *ncProvider) ResolveSizing(_ string, _ interfaces.Size, _ *interfaces.ResourceHints) (*interfaces.ProviderSizing, error) {
	return nil, nil
}

func (p *ncProvider) SupportedCanonicalKeys() []string {
	return interfaces.CanonicalKeys()
}

func (p *ncProvider) BootstrapStateBackend(_ context.Context, _ map[string]any) (*interfaces.BootstrapResult, error) {
	return nil, nil
}

func (p *ncProvider) Close() error { return nil }

// ---- Marshalling helpers (pb ↔ Go) ----
// These mirror the helpers in workflow-plugin-digitalocean/internal/iacserver.go.

func unmarshalJSONMapNC(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func marshalJSONMapNC(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

func marshalJSONAnyNC(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func unmarshalJSONAnyNC(b []byte) (any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func strVal(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func refFromPBNC(r *pb.ResourceRef) interfaces.ResourceRef {
	if r == nil {
		return interfaces.ResourceRef{}
	}
	return interfaces.ResourceRef{Name: r.GetName(), Type: r.GetType(), ProviderID: r.GetProviderId()}
}

func refsFromPBNC(refs []*pb.ResourceRef) []interfaces.ResourceRef {
	out := make([]interfaces.ResourceRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, refFromPBNC(r))
	}
	return out
}

func hintsToPBNC(h *interfaces.ResourceHints) *pb.ResourceHints {
	if h == nil {
		return nil
	}
	return &pb.ResourceHints{Cpu: h.CPU, Memory: h.Memory, Storage: h.Storage}
}

func hintsFromPBNC(h *pb.ResourceHints) *interfaces.ResourceHints {
	if h == nil {
		return nil
	}
	return &interfaces.ResourceHints{CPU: h.GetCpu(), Memory: h.GetMemory(), Storage: h.GetStorage()}
}

func specFromPBNC(s *pb.ResourceSpec) (interfaces.ResourceSpec, error) {
	if s == nil {
		return interfaces.ResourceSpec{}, nil
	}
	cfg, err := unmarshalJSONMapNC(s.GetConfigJson())
	if err != nil {
		return interfaces.ResourceSpec{}, err
	}
	return interfaces.ResourceSpec{
		Name:      s.GetName(),
		Type:      s.GetType(),
		Config:    cfg,
		Size:      interfaces.Size(s.GetSize()),
		Hints:     hintsFromPBNC(s.GetHints()),
		DependsOn: append([]string(nil), s.GetDependsOn()...),
	}, nil
}

func specsFromPBNC(specs []*pb.ResourceSpec) ([]interfaces.ResourceSpec, error) {
	out := make([]interfaces.ResourceSpec, 0, len(specs))
	for _, s := range specs {
		gs, err := specFromPBNC(s)
		if err != nil {
			return nil, err
		}
		out = append(out, gs)
	}
	return out, nil
}

func specToPBNC(s interfaces.ResourceSpec) (*pb.ResourceSpec, error) {
	cfgJSON, err := marshalJSONMapNC(s.Config)
	if err != nil {
		return nil, err
	}
	return &pb.ResourceSpec{
		Name:       s.Name,
		Type:       s.Type,
		ConfigJson: cfgJSON,
		Size:       string(s.Size),
		Hints:      hintsToPBNC(s.Hints),
		DependsOn:  append([]string(nil), s.DependsOn...),
	}, nil
}

func stateFromPBNC(s *pb.ResourceState) (*interfaces.ResourceState, error) {
	if s == nil {
		return nil, nil
	}
	applied, err := unmarshalJSONMapNC(s.GetAppliedConfigJson())
	if err != nil {
		return nil, err
	}
	outputs, err := unmarshalJSONMapNC(s.GetOutputsJson())
	if err != nil {
		return nil, err
	}
	return &interfaces.ResourceState{
		ID:            s.GetId(),
		Name:          s.GetName(),
		Type:          s.GetType(),
		Provider:      s.GetProvider(),
		ProviderRef:   s.GetProviderRef(),
		ProviderID:    s.GetProviderId(),
		ConfigHash:    s.GetConfigHash(),
		AppliedConfig: applied,
		Outputs:       outputs,
		Dependencies:  append([]string(nil), s.GetDependencies()...),
		CreatedAt:     timeFmPBNC(s.GetCreatedAt()),
		UpdatedAt:     timeFmPBNC(s.GetUpdatedAt()),
	}, nil
}

func statesFromPBNC(states []*pb.ResourceState) ([]interfaces.ResourceState, error) {
	out := make([]interfaces.ResourceState, 0, len(states))
	for _, s := range states {
		gs, err := stateFromPBNC(s)
		if err != nil {
			return nil, err
		}
		if gs != nil {
			out = append(out, *gs)
		}
	}
	return out, nil
}

func stateToPBNC(st *interfaces.ResourceState) (*pb.ResourceState, error) {
	if st == nil {
		return nil, nil
	}
	appliedJSON, err := marshalJSONMapNC(st.AppliedConfig)
	if err != nil {
		return nil, err
	}
	outputsJSON, err := marshalJSONMapNC(st.Outputs)
	if err != nil {
		return nil, err
	}
	return &pb.ResourceState{
		Id:                st.ID,
		Name:              st.Name,
		Type:              st.Type,
		Provider:          st.Provider,
		ProviderRef:       st.ProviderRef,
		ProviderId:        st.ProviderID,
		ConfigHash:        st.ConfigHash,
		AppliedConfigJson: appliedJSON,
		OutputsJson:       outputsJSON,
		Dependencies:      append([]string(nil), st.Dependencies...),
		CreatedAt:         timeToPBNC(st.CreatedAt),
		UpdatedAt:         timeToPBNC(st.UpdatedAt),
	}, nil
}

func changesToPBNC(changes []interfaces.FieldChange) ([]*pb.FieldChange, error) {
	out := make([]*pb.FieldChange, 0, len(changes))
	for _, c := range changes {
		oldJSON, err := marshalJSONAnyNC(c.Old)
		if err != nil {
			return nil, err
		}
		newJSON, err := marshalJSONAnyNC(c.New)
		if err != nil {
			return nil, err
		}
		out = append(out, &pb.FieldChange{
			Path:     c.Path,
			OldJson:  oldJSON,
			NewJson:  newJSON,
			ForceNew: c.ForceNew,
		})
	}
	return out, nil
}

func changesFromPBNC(changes []*pb.FieldChange) ([]interfaces.FieldChange, error) {
	out := make([]interfaces.FieldChange, 0, len(changes))
	for _, c := range changes {
		oldVal, err := unmarshalJSONAnyNC(c.GetOldJson())
		if err != nil {
			return nil, err
		}
		newVal, err := unmarshalJSONAnyNC(c.GetNewJson())
		if err != nil {
			return nil, err
		}
		out = append(out, interfaces.FieldChange{
			Path:     c.GetPath(),
			Old:      oldVal,
			New:      newVal,
			ForceNew: c.GetForceNew(),
		})
	}
	return out, nil
}

func planActionToPBNC(a interfaces.PlanAction) (*pb.PlanAction, error) {
	pbSpec, err := specToPBNC(a.Resource)
	if err != nil {
		return nil, err
	}
	var pbCurrent *pb.ResourceState
	if a.Current != nil {
		pbCurrent, err = stateToPBNC(a.Current)
		if err != nil {
			return nil, err
		}
	}
	pbChanges, err := changesToPBNC(a.Changes)
	if err != nil {
		return nil, err
	}
	return &pb.PlanAction{
		Action:             a.Action,
		Resource:           pbSpec,
		Current:            pbCurrent,
		Changes:            pbChanges,
		ResolvedConfigHash: a.ResolvedConfigHash,
	}, nil
}

func planActionFromPBNC(a *pb.PlanAction) (interfaces.PlanAction, error) {
	if a == nil {
		return interfaces.PlanAction{}, nil
	}
	spec, err := specFromPBNC(a.GetResource())
	if err != nil {
		return interfaces.PlanAction{}, err
	}
	var current *interfaces.ResourceState
	if a.GetCurrent() != nil {
		current, err = stateFromPBNC(a.GetCurrent())
		if err != nil {
			return interfaces.PlanAction{}, err
		}
	}
	changes, err := changesFromPBNC(a.GetChanges())
	if err != nil {
		return interfaces.PlanAction{}, err
	}
	return interfaces.PlanAction{
		Action:             a.GetAction(),
		Resource:           spec,
		Current:            current,
		Changes:            changes,
		ResolvedConfigHash: a.GetResolvedConfigHash(),
	}, nil
}

func planToPBNC(p *interfaces.IaCPlan) (*pb.IaCPlan, error) {
	if p == nil {
		return nil, nil
	}
	pbActions := make([]*pb.PlanAction, 0, len(p.Actions))
	for i := range p.Actions {
		pa, err := planActionToPBNC(p.Actions[i])
		if err != nil {
			return nil, err
		}
		pbActions = append(pbActions, pa)
	}
	if p.SchemaVersion < math.MinInt32 || p.SchemaVersion > math.MaxInt32 {
		return nil, fmt.Errorf("namecheap iacserver: plan SchemaVersion %d out of int32 range", p.SchemaVersion)
	}
	return &pb.IaCPlan{
		Id:            p.ID,
		Actions:       pbActions,
		CreatedAt:     timeToPBNC(p.CreatedAt),
		DesiredHash:   p.DesiredHash,
		SchemaVersion: int32(p.SchemaVersion), //nolint:gosec // G115: range-checked above
		InputSnapshot: copyStringMapNC(p.InputSnapshot),
	}, nil
}

// planFromPBNC is the inverse of planToPBNC. Currently exercised only
// by the iacserver round-trip test; kept here (rather than _test.go)
// so it can be shared by future client-side bridge code if needed.
func planFromPBNC(p *pb.IaCPlan) (*interfaces.IaCPlan, error) {
	if p == nil {
		return nil, nil
	}
	actions := make([]interfaces.PlanAction, 0, len(p.GetActions()))
	for _, a := range p.GetActions() {
		pa, err := planActionFromPBNC(a)
		if err != nil {
			return nil, err
		}
		actions = append(actions, pa)
	}
	return &interfaces.IaCPlan{
		ID:            p.GetId(),
		Actions:       actions,
		CreatedAt:     timeFmPBNC(p.GetCreatedAt()),
		DesiredHash:   p.GetDesiredHash(),
		SchemaVersion: int(p.GetSchemaVersion()),
		InputSnapshot: copyStringMapNC(p.GetInputSnapshot()),
	}, nil
}

func timeToPBNC(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func timeFmPBNC(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime()
}

func copyStringMapNC(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
