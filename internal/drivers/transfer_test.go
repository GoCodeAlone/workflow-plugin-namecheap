package drivers

import (
	"context"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"
)

type fakeTransferClient struct {
	created []TransferCreateArgs
	status  *TransferStatus
}

func (f *fakeTransferClient) CreateTransfer(_ context.Context, args TransferCreateArgs) (*TransferCreateResult, error) {
	f.created = append(f.created, args)
	return &TransferCreateResult{
		Domain:        args.Domain,
		Transfer:      true,
		TransferID:    "15",
		StatusID:      "-1",
		StatusCode:    "2",
		OrderID:       "575",
		TransactionID: "759",
		ChargedAmount: "8.88",
	}, nil
}

func (f *fakeTransferClient) GetTransferStatus(_ context.Context, transferID string) (*TransferStatus, error) {
	if f.status != nil {
		out := *f.status
		if out.TransferID == "" {
			out.TransferID = transferID
		}
		return &out, nil
	}
	return &TransferStatus{TransferID: transferID, Status: "Queued for submission", StatusID: "-1"}, nil
}

func TestTransferDriver_CreateRequiresConfirmation(t *testing.T) {
	driver := NewTransferDriverWithClient(&fakeTransferClient{})
	_, err := driver.Create(context.Background(), interfaces.ResourceSpec{
		Name: "example-com-transfer",
		Type: "infra.domain_transfer",
		Config: map[string]any{
			"domain":   "example.com",
			"years":    1,
			"epp_code": "secret-code",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "confirm_transfer") {
		t.Fatalf("Create err = %v, want confirmation refusal", err)
	}
}

func TestTransferDriver_CreateStartsTransferAndOmitsEPPCode(t *testing.T) {
	fake := &fakeTransferClient{}
	driver := NewTransferDriverWithClient(fake)
	out, err := driver.Create(context.Background(), interfaces.ResourceSpec{
		Name: "example-com-transfer",
		Type: "infra.domain_transfer",
		Config: map[string]any{
			"domain":           "example.com",
			"years":            1,
			"epp_code":         "secret-code",
			"confirm_transfer": true,
			"promotion_code":   "SAVE",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(fake.created) != 1 || fake.created[0].EPPCode != "secret-code" || fake.created[0].PromotionCode != "SAVE" {
		t.Fatalf("created = %#v", fake.created)
	}
	if out.ProviderID != "15" || out.Outputs["charged_amount"] != "8.88" {
		t.Fatalf("out = %#v", out)
	}
	if _, ok := out.Outputs["epp_code"]; ok {
		t.Fatalf("outputs leaked epp_code: %#v", out.Outputs)
	}
}

func TestTransferDriver_ReadUsesTransferID(t *testing.T) {
	driver := NewTransferDriverWithClient(&fakeTransferClient{status: &TransferStatus{Status: "Pending owner approval", StatusID: "1"}})
	out, err := driver.Read(context.Background(), interfaces.ResourceRef{Name: "example-com-transfer", Type: "infra.domain_transfer", ProviderID: "15"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.ProviderID != "15" || out.Outputs["status"] != "Pending owner approval" {
		t.Fatalf("out = %#v", out)
	}
}

func TestTransferDriver_DeleteRefusesCancellation(t *testing.T) {
	driver := NewTransferDriverWithClient(&fakeTransferClient{})
	err := driver.Delete(context.Background(), interfaces.ResourceRef{Name: "example-com-transfer", Type: "infra.domain_transfer", ProviderID: "15"})
	if err == nil || !strings.Contains(err.Error(), "refuses to cancel") {
		t.Fatalf("Delete err = %v, want cancellation refusal", err)
	}
}

func TestTransferDriver_DiffDetectsDomainReplacement(t *testing.T) {
	driver := NewTransferDriverWithClient(&fakeTransferClient{})
	diff, err := driver.Diff(context.Background(), interfaces.ResourceSpec{
		Name: "example-com-transfer",
		Type: "infra.domain_transfer",
		Config: map[string]any{
			"domain":           "new.example",
			"years":            1,
			"epp_code":         "secret-code",
			"confirm_transfer": true,
		},
	}, &interfaces.ResourceOutput{
		Name:       "example-com-transfer",
		Type:       "infra.domain_transfer",
		ProviderID: "15",
		Outputs: map[string]any{
			"domain": "old.example",
		},
	})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !diff.NeedsReplace || len(diff.Changes) != 1 || diff.Changes[0].Path != "domain" {
		t.Fatalf("diff = %#v, want domain replacement", diff)
	}
}

func TestTransferDriver_DiffRejectsFractionalYears(t *testing.T) {
	driver := NewTransferDriverWithClient(&fakeTransferClient{})
	_, err := driver.Diff(context.Background(), interfaces.ResourceSpec{
		Name: "example-com-transfer",
		Type: "infra.domain_transfer",
		Config: map[string]any{
			"domain":           "example.com",
			"years":            1.5,
			"epp_code":         "secret-code",
			"confirm_transfer": true,
		},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "years must be an integer") {
		t.Fatalf("Diff err = %v, want integer validation", err)
	}
}
