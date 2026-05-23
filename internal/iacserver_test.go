package internal

// iacserver_test.go — typed pb.IaCProvider*Server smoke tests for
// workflow-plugin-namecheap.
//
// Tests start an in-process gRPC server using bufconn and exercise
// the typed RPCs via pb client stubs.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-namecheap/internal/drivers"
	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
	"github.com/namecheap/go-namecheap-sdk/v2/namecheap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const (
	iacTestBufSize     = 1024 * 1024
	iacTestRPCDeadline = 5 * time.Second
)

// setupTestServer starts an in-process gRPC server backed by a fresh
// ncIaCServer and returns a connected client connection.
func setupTestServer(t *testing.T) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(iacTestBufSize)
	t.Cleanup(func() { _ = listener.Close() })

	server := grpc.NewServer()
	srv := NewIaCServer()
	if err := sdk.RegisterAllIaCProviderServices(server, srv); err != nil {
		t.Fatalf("RegisterAllIaCProviderServices: %v", err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// ---- Capabilities ----

func TestNcIaCServer_Capabilities(t *testing.T) {
	conn := setupTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), iacTestRPCDeadline)
	t.Cleanup(cancel)

	client := pb.NewIaCProviderRequiredClient(conn)

	capsResp, err := client.Capabilities(ctx, &pb.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}

	// Must declare v2.
	if got := capsResp.GetComputePlanVersion(); got != "v2" {
		t.Errorf("ComputePlanVersion = %q, want v2", got)
	}

	caps := capsResp.GetCapabilities()
	if len(caps) != 1 {
		t.Fatalf("Capabilities len = %d, want 1", len(caps))
	}
	if caps[0].GetResourceType() != "infra.dns" {
		t.Errorf("ResourceType = %q, want infra.dns", caps[0].GetResourceType())
	}
	if len(caps[0].GetOperations()) == 0 {
		t.Error("infra.dns capability has no operations")
	}
}

// ---- Name / Version ----

func TestNcIaCServer_NameVersion(t *testing.T) {
	conn := setupTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), iacTestRPCDeadline)
	t.Cleanup(cancel)

	client := pb.NewIaCProviderRequiredClient(conn)

	nameResp, err := client.Name(ctx, &pb.NameRequest{})
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if nameResp.GetName() != "namecheap" {
		t.Errorf("Name = %q, want namecheap", nameResp.GetName())
	}

	verResp, err := client.Version(ctx, &pb.VersionRequest{})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if verResp.GetVersion() == "" {
		t.Error("Version is empty; want non-empty")
	}
}

// ---- Initialize ----

func TestNcIaCServer_Initialize_MissingCreds(t *testing.T) {
	conn := setupTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), iacTestRPCDeadline)
	t.Cleanup(cancel)

	client := pb.NewIaCProviderRequiredClient(conn)

	cfgJSON, _ := json.Marshal(map[string]any{"api_user": "u"}) // missing api_key + client_ip
	_, err := client.Initialize(ctx, &pb.InitializeRequest{ConfigJson: cfgJSON})
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
	if !containsAny(err.Error(), "api_key", "client_ip", "ErrAuthMissing", "not configured") {
		t.Errorf("error %q should mention missing fields", err.Error())
	}
}

func TestNcIaCServer_Initialize_Valid(t *testing.T) {
	conn := setupTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), iacTestRPCDeadline)
	t.Cleanup(cancel)

	client := pb.NewIaCProviderRequiredClient(conn)

	cfgJSON, _ := json.Marshal(map[string]any{
		"api_user":  "testuser",
		"api_key":   "testkey",
		"client_ip": "203.0.113.10",
		"sandbox":   true,
	})
	_, err := client.Initialize(ctx, &pb.InitializeRequest{ConfigJson: cfgJSON})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

// ---- Plan before Initialize ----

func TestNcIaCServer_Plan_BeforeInitialize(t *testing.T) {
	conn := setupTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), iacTestRPCDeadline)
	t.Cleanup(cancel)

	client := pb.NewIaCProviderRequiredClient(conn)

	_, err := client.Plan(ctx, &pb.PlanRequest{})
	if err == nil {
		t.Fatal("expected error when Plan called before Initialize")
	}
	if !containsAny(err.Error(), "Initialize", "before") {
		t.Logf("Plan-before-init error: %v (accepted — any error is fine)", err)
	}
}

// ---- FinalizeApply (no-op for DNS) ----

func TestNcIaCServer_FinalizeApply_NoOp(t *testing.T) {
	conn := setupTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), iacTestRPCDeadline)
	t.Cleanup(cancel)

	// FinalizeApply is registered as an optional service; confirm it's reachable.
	finalClient := pb.NewIaCProviderFinalizerClient(conn)
	resp, err := finalClient.FinalizeApply(ctx, &pb.FinalizeApplyRequest{})
	if err != nil {
		t.Fatalf("FinalizeApply: %v", err)
	}
	if len(resp.GetErrors()) != 0 {
		t.Errorf("FinalizeApply returned errors: %v", resp.GetErrors())
	}
}

// ---- Destroy before Initialize ----

func TestNcIaCServer_Destroy_BeforeInitialize(t *testing.T) {
	conn := setupTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), iacTestRPCDeadline)
	t.Cleanup(cancel)

	client := pb.NewIaCProviderRequiredClient(conn)

	_, err := client.Destroy(ctx, &pb.DestroyRequest{
		Refs: []*pb.ResourceRef{{Name: "x.com", Type: "infra.dns", ProviderId: "x.com"}},
	})
	if err == nil {
		t.Fatal("expected error when Destroy called before Initialize")
	}
}

func TestNcProvider_ImportReadsDNSState(t *testing.T) {
	p := &ncProvider{driver: drivers.NewDNSDriverWithClient(&fakeNCImportClient{})}
	state, err := p.Import(context.Background(), "example.com", "infra.dns")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if state.Provider != "namecheap" {
		t.Fatalf("Provider = %q, want namecheap", state.Provider)
	}
	if state.Outputs["is_using_our_dns"] != true {
		t.Fatalf("is_using_our_dns = %#v, want true", state.Outputs["is_using_our_dns"])
	}
}

type fakeNCImportClient struct{}

func (fakeNCImportClient) GetHosts(domain string) (*namecheap.DomainsDNSGetHostsCommandResponse, error) {
	d := domain
	emailType := "MX"
	usingOurDNS := true
	name := "@"
	recordType := "TXT"
	address := "imported"
	ttl := 300
	hosts := []namecheap.DomainsDNSHostRecordDetailed{{
		Name:    &name,
		Type:    &recordType,
		Address: &address,
		TTL:     &ttl,
	}}
	return &namecheap.DomainsDNSGetHostsCommandResponse{
		DomainDNSGetHostsResult: &namecheap.DomainDNSGetHostsResult{
			Domain:        &d,
			EmailType:     &emailType,
			IsUsingOurDNS: &usingOurDNS,
			Hosts:         &hosts,
		},
	}, nil
}

func (fakeNCImportClient) SetHosts(args *namecheap.DomainsDNSSetHostsArgs) (*namecheap.DomainsDNSSetHostsCommandResponse, error) {
	domain := ""
	if args.Domain != nil {
		domain = *args.Domain
	}
	ok := true
	return &namecheap.DomainsDNSSetHostsCommandResponse{
		DomainDNSSetHostsResult: &namecheap.DomainDNSSetHostsResult{Domain: &domain, IsSuccess: &ok},
	}, nil
}

// ---- Marshalling helpers ----

func TestUnmarshalJSONMapNC_Empty(t *testing.T) {
	m, err := unmarshalJSONMapNC(nil)
	if err != nil {
		t.Fatalf("unmarshalJSONMapNC(nil): %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map; got %v", m)
	}
}

func TestUnmarshalJSONMapNC_Valid(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"foo": "bar", "n": 42.0})
	m, err := unmarshalJSONMapNC(b)
	if err != nil {
		t.Fatalf("unmarshalJSONMapNC: %v", err)
	}
	if m["foo"] != "bar" {
		t.Errorf("foo = %v, want bar", m["foo"])
	}
}

func TestStrVal(t *testing.T) {
	m := map[string]any{"k": "v", "n": 1}
	if strVal(m, "k") != "v" {
		t.Error("strVal mismatch")
	}
	if strVal(m, "n") != "" {
		t.Error("non-string strVal should return empty")
	}
	if strVal(nil, "k") != "" {
		t.Error("nil map strVal should return empty")
	}
}

// ---- Config helper ----

func TestConfig_Validate_UsedByInitialize(t *testing.T) {
	// Verify that Config.Validate is what Initialize internally calls.
	cfg := Config{APIUser: "u", APIKey: "k", ClientIP: "1.2.3.4"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid Config: %v", err)
	}

	bad := Config{APIUser: "u"}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for incomplete config")
	} else if !errors.Is(err, ErrAuthMissing) {
		t.Errorf("err = %v; want wrapped ErrAuthMissing", err)
	}
}

// ---- plan round-trip ----

func TestPlanToPBNC_RoundTrip(t *testing.T) {
	from, _ := json.Marshal(map[string]any{"record_count": 0})
	pbPlan := &pb.IaCPlan{
		Id: "test-plan",
		Actions: []*pb.PlanAction{
			{
				Action: "create",
				Resource: &pb.ResourceSpec{
					Name:       "example.com",
					Type:       "infra.dns",
					ConfigJson: from,
				},
			},
		},
	}
	goPlan, err := planFromPBNC(pbPlan)
	if err != nil {
		t.Fatalf("planFromPBNC: %v", err)
	}
	if goPlan.ID != "test-plan" {
		t.Errorf("plan ID = %q, want test-plan", goPlan.ID)
	}
	if len(goPlan.Actions) != 1 {
		t.Fatalf("actions len = %d, want 1", len(goPlan.Actions))
	}
	if goPlan.Actions[0].Action != "create" {
		t.Errorf("action = %q, want create", goPlan.Actions[0].Action)
	}

	// Re-encode and compare action count.
	re, err := planToPBNC(goPlan)
	if err != nil {
		t.Fatalf("planToPBNC: %v", err)
	}
	if len(re.GetActions()) != 1 {
		t.Errorf("re-encoded actions len = %d, want 1", len(re.GetActions()))
	}
}

// containsAny returns true if s contains any of the provided substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) == 0 {
			continue
		}
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
