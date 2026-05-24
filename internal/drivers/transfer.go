package drivers

import (
	"context"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/GoCodeAlone/workflow/interfaces"
	"github.com/namecheap/go-namecheap-sdk/v2/namecheap"
)

const transferResourceType = "infra.domain_transfer"

type TransferClient interface {
	CreateTransfer(ctx context.Context, args TransferCreateArgs) (*TransferCreateResult, error)
	GetTransferStatus(ctx context.Context, transferID string) (*TransferStatus, error)
}

type TransferCreateArgs struct {
	Domain            string
	Years             int
	EPPCode           string
	PromotionCode     string
	AddFreeWhoisguard *bool
	WhoisguardEnabled *bool
}

type TransferCreateResult struct {
	Domain        string
	Transfer      bool
	TransferID    string
	StatusID      string
	StatusCode    string
	OrderID       string
	TransactionID string
	ChargedAmount string
}

type TransferStatus struct {
	TransferID string
	Status     string
	StatusID   string
}

type TransferDriver struct {
	client TransferClient
}

func NewTransferDriver(c *namecheap.Client) *TransferDriver {
	return &TransferDriver{client: &realTransferClient{client: c}}
}

func NewTransferDriverWithClient(c TransferClient) *TransferDriver {
	return &TransferDriver{client: c}
}

func (d *TransferDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("domain transfer create %q: %w", spec.Name, err)
	}
	parsed, err := parseTransferSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("domain transfer create %q: %w", spec.Name, err)
	}
	if !parsed.ConfirmTransfer {
		return nil, fmt.Errorf("domain transfer create %q: confirm_transfer must be true because Namecheap transfer creation places a chargeable order", spec.Name)
	}
	result, err := d.client.CreateTransfer(ctx, parsed.TransferCreateArgs)
	if err != nil {
		return nil, fmt.Errorf("domain transfer create %q: %w", spec.Name, err)
	}
	return transferCreateOutput(spec.Name, result), nil
}

func (d *TransferDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("domain transfer read %q: %w", ref.Name, err)
	}
	transferID := strings.TrimSpace(ref.ProviderID)
	if transferID == "" {
		transferID = strings.TrimSpace(ref.Name)
	}
	if transferID == "" {
		return nil, fmt.Errorf("domain transfer read %q: transfer id is required", ref.Name)
	}
	status, err := d.client.GetTransferStatus(ctx, transferID)
	if err != nil {
		return nil, fmt.Errorf("domain transfer read %q: %w", ref.Name, err)
	}
	return transferStatusOutput(ref.Name, status), nil
}

func (d *TransferDriver) Update(ctx context.Context, ref interfaces.ResourceRef, _ interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	return d.Read(ctx, ref)
}

func (d *TransferDriver) Delete(_ context.Context, ref interfaces.ResourceRef) error {
	transferID := ref.ProviderID
	if transferID == "" {
		transferID = ref.Name
	}
	return fmt.Errorf("domain transfer delete %q: infra.domain_transfer refuses to cancel transfers; review the transfer in Namecheap before changing status", transferID)
}

func (d *TransferDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	parsed, err := parseTransferSpec(desired)
	if err != nil {
		return nil, fmt.Errorf("domain transfer diff: %w", err)
	}
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	currentDomain, _ := current.Outputs["domain"].(string)
	if currentDomain != "" && !strings.EqualFold(currentDomain, parsed.Domain) {
		return &interfaces.DiffResult{
			NeedsUpdate:  true,
			NeedsReplace: true,
			Changes: []interfaces.FieldChange{{
				Path:     "domain",
				Old:      currentDomain,
				New:      parsed.Domain,
				ForceNew: true,
			}},
		}, nil
	}
	return &interfaces.DiffResult{}, nil
}

func (d *TransferDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	out, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	status, _ := out.Outputs["status"].(string)
	return &interfaces.HealthResult{Healthy: !strings.EqualFold(status, "failed"), Message: status}, nil
}

func (d *TransferDriver) Scale(ctx context.Context, ref interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return d.Read(ctx, ref)
}

func (d *TransferDriver) Type() string { return transferResourceType }

func (d *TransferDriver) SensitiveKeys() []string { return []string{"epp_code"} }

func (d *TransferDriver) ProviderIDFormat() interfaces.ProviderIDFormat {
	return interfaces.IDFormatFreeform
}

type transferSpec struct {
	TransferCreateArgs
	ConfirmTransfer bool
}

func parseTransferSpec(spec interfaces.ResourceSpec) (transferSpec, error) {
	domain, _ := spec.Config["domain"].(string)
	if domain == "" {
		domain = spec.Name
	}
	domain = strings.TrimSuffix(strings.TrimSpace(domain), ".")
	if domain == "" || !interfaces.ValidateProviderID(domain, interfaces.IDFormatDomainName) {
		return transferSpec{}, fmt.Errorf("domain %q is not a valid domain name", domain)
	}
	years, err := intFromConfig(spec.Config, "years", 1)
	if err != nil {
		return transferSpec{}, err
	}
	if years != 1 {
		return transferSpec{}, fmt.Errorf("years must be 1 for Namecheap transfer API")
	}
	epp, _ := spec.Config["epp_code"].(string)
	if strings.TrimSpace(epp) == "" {
		return transferSpec{}, fmt.Errorf("epp_code is required")
	}
	parsed := transferSpec{
		TransferCreateArgs: TransferCreateArgs{
			Domain:        domain,
			Years:         years,
			EPPCode:       strings.TrimSpace(epp),
			PromotionCode: strings.TrimSpace(stringFromConfig(spec.Config, "promotion_code")),
		},
		ConfirmTransfer: boolFromConfig(spec.Config, "confirm_transfer"),
	}
	if v, ok, err := optionalBoolFromConfig(spec.Config, "add_free_whoisguard"); err != nil {
		return transferSpec{}, err
	} else if ok {
		parsed.AddFreeWhoisguard = &v
	}
	if v, ok, err := optionalBoolFromConfig(spec.Config, "wg_enabled"); err != nil {
		return transferSpec{}, err
	} else if ok {
		parsed.WhoisguardEnabled = &v
	}
	return parsed, nil
}

func intFromConfig(config map[string]any, key string, defaultValue int) (int, error) {
	raw, ok := config[key]
	if !ok {
		return defaultValue, nil
	}
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return int(v), nil
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
}

func stringFromConfig(config map[string]any, key string) string {
	v, _ := config[key].(string)
	return v
}

func boolFromConfig(config map[string]any, key string) bool {
	v, _ := config[key].(bool)
	return v
}

func optionalBoolFromConfig(config map[string]any, key string) (bool, bool, error) {
	raw, ok := config[key]
	if !ok {
		return false, false, nil
	}
	v, ok := raw.(bool)
	if !ok {
		return false, false, fmt.Errorf("%s must be a bool", key)
	}
	return v, true, nil
}

func transferCreateOutput(name string, result *TransferCreateResult) *interfaces.ResourceOutput {
	if result == nil {
		result = &TransferCreateResult{}
	}
	outputs := map[string]any{
		"domain":         result.Domain,
		"transfer":       result.Transfer,
		"transfer_id":    result.TransferID,
		"status_id":      result.StatusID,
		"status_code":    result.StatusCode,
		"order_id":       result.OrderID,
		"transaction_id": result.TransactionID,
		"charged_amount": result.ChargedAmount,
	}
	status := "pending"
	if !result.Transfer {
		status = "failed"
	}
	return &interfaces.ResourceOutput{Name: name, Type: transferResourceType, ProviderID: result.TransferID, Outputs: outputs, Status: status}
}

func transferStatusOutput(name string, status *TransferStatus) *interfaces.ResourceOutput {
	if status == nil {
		status = &TransferStatus{}
	}
	outputs := map[string]any{
		"transfer_id": status.TransferID,
		"status":      status.Status,
		"status_id":   status.StatusID,
	}
	return &interfaces.ResourceOutput{Name: name, Type: transferResourceType, ProviderID: status.TransferID, Outputs: outputs, Status: status.Status}
}

type realTransferClient struct {
	client *namecheap.Client
}

func (c *realTransferClient) CreateTransfer(ctx context.Context, args TransferCreateArgs) (*TransferCreateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params := map[string]string{
		"Command":    "namecheap.domains.transfer.create",
		"DomainName": args.Domain,
		"Years":      strconv.Itoa(args.Years),
		"EPPCode":    args.EPPCode,
	}
	if args.PromotionCode != "" {
		params["PromotionCode"] = args.PromotionCode
	}
	if args.AddFreeWhoisguard != nil {
		params["AddFreeWhoisguard"] = yesNo(*args.AddFreeWhoisguard)
	}
	if args.WhoisguardEnabled != nil {
		params["WGenable"] = yesNo(*args.WhoisguardEnabled)
	}
	var response transferCreateResponse
	_, err := c.client.DoXML(params, &response)
	if err != nil {
		return nil, err
	}
	if err := firstNamecheapError(response.Errors); err != nil {
		return nil, err
	}
	if response.CommandResponse == nil || response.CommandResponse.Result == nil {
		return nil, fmt.Errorf("namecheap transfer create: missing result")
	}
	return transferCreateResultFromXML(response.CommandResponse.Result), nil
}

func (c *realTransferClient) GetTransferStatus(ctx context.Context, transferID string) (*TransferStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params := map[string]string{
		"Command":    "namecheap.domains.transfer.getStatus",
		"TransferID": transferID,
	}
	var response transferStatusResponse
	_, err := c.client.DoXML(params, &response)
	if err != nil {
		return nil, err
	}
	if err := firstNamecheapError(response.Errors); err != nil {
		return nil, err
	}
	if response.CommandResponse == nil || response.CommandResponse.Result == nil {
		return nil, fmt.Errorf("namecheap transfer status: missing result")
	}
	return &TransferStatus{
		TransferID: response.CommandResponse.Result.TransferID,
		Status:     response.CommandResponse.Result.Status,
		StatusID:   response.CommandResponse.Result.StatusID,
	}, nil
}

type transferCreateResponse struct {
	XMLName         xml.Name                       `xml:"ApiResponse"`
	Errors          []namecheapAPIError            `xml:"Errors>Error"`
	CommandResponse *transferCreateCommandResponse `xml:"CommandResponse"`
}

type transferCreateCommandResponse struct {
	Result *transferCreateXMLResult `xml:"DomainTransferCreateResult"`
}

type transferCreateXMLResult struct {
	Domain        string `xml:"Domainname,attr"`
	Transfer      bool   `xml:"Transfer,attr"`
	TransferID    string `xml:"TransferID,attr"`
	StatusID      string `xml:"StatusID,attr"`
	OrderID       string `xml:"OrderID,attr"`
	TransactionID string `xml:"TransactionID,attr"`
	ChargedAmount string `xml:"ChargedAmount,attr"`
	StatusCode    string `xml:"StatusCode,attr"`
}

type transferStatusResponse struct {
	XMLName         xml.Name                       `xml:"ApiResponse"`
	Errors          []namecheapAPIError            `xml:"Errors>Error"`
	CommandResponse *transferStatusCommandResponse `xml:"CommandResponse"`
}

type transferStatusCommandResponse struct {
	Result *transferStatusXMLResult `xml:"DomainTransferGetStatusResult"`
}

type transferStatusXMLResult struct {
	TransferID string `xml:"TransferID,attr"`
	Status     string `xml:"Status,attr"`
	StatusID   string `xml:"StatusID,attr"`
}

type namecheapAPIError struct {
	Message string `xml:",chardata"`
	Number  string `xml:"Number,attr"`
}

func firstNamecheapError(errors []namecheapAPIError) error {
	if len(errors) == 0 {
		return nil
	}
	apiErr := errors[0]
	return fmt.Errorf("%s (%s)", strings.TrimSpace(apiErr.Message), apiErr.Number)
}

func transferCreateResultFromXML(result *transferCreateXMLResult) *TransferCreateResult {
	return &TransferCreateResult{
		Domain:        result.Domain,
		Transfer:      result.Transfer,
		TransferID:    result.TransferID,
		StatusID:      result.StatusID,
		StatusCode:    result.StatusCode,
		OrderID:       result.OrderID,
		TransactionID: result.TransactionID,
		ChargedAmount: result.ChargedAmount,
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
