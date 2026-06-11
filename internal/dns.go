package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrAuthMissing is returned by Configure when required API
// credentials are missing.
var ErrAuthMissing = errors.New("namecheap: api_user, api_key, or client_ip not configured")

// Config is the provider-side config block.
type Config struct {
	APIUser  string // matches `api_user` YAML key
	APIKey   string // matches `api_key`
	ClientIP string // matches `client_ip`
	Sandbox  bool   // when true, use api.sandbox.namecheap.com
}

// Validate ensures every required field is non-empty + ClientIP is
// a syntactically valid IPv4/IPv6.
func (c Config) Validate() error {
	if strings.TrimSpace(c.APIUser) == "" {
		return fmt.Errorf("%w: api_user is empty", ErrAuthMissing)
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("%w: api_key is empty", ErrAuthMissing)
	}
	if strings.TrimSpace(c.ClientIP) == "" {
		return fmt.Errorf("%w: client_ip is empty (required for Namecheap API allowlist)", ErrAuthMissing)
	}
	if net.ParseIP(c.ClientIP) == nil {
		return fmt.Errorf("namecheap: client_ip %q is not a valid IP address (CIDR is unsupported by Namecheap)", c.ClientIP)
	}
	return nil
}

// DNSRecord is the canonical wfctl shape.
type DNSRecord struct {
	Type string // A | AAAA | CNAME | MX | TXT | NS | CAA
	Name string // subdomain or "@" for apex
	Data string // record value
	TTL  int    // seconds; min 60, default 1800
	MX   int    // MX priority; 0 except for MX records
}

// DNSSpec is the parsed `infra.dns` resource spec.
type DNSSpec struct {
	Domain  string
	Records []DNSRecord
}

// Driver implements interfaces.ResourceDriver for infra.dns. The
// gRPC SDK glue lives in serve.go.
type Driver struct {
	cfg Config
	// client is the namecheap.Client; injected as `any` here to avoid
	// pulling the SDK into the type signature pre-vendor.
	client any
}

// NewDriver returns a Driver bound to the given config.
func NewDriver(cfg Config) (*Driver, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Driver{cfg: cfg}, nil
}

// Configure (re-)applies the config to the underlying client.
func (d *Driver) Configure(_ context.Context, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	d.cfg = cfg
	d.client = nil // reset; lazy init on first call.
	return nil
}

// Type returns the IaC resource type this driver handles.
func (d *Driver) Type() string { return "infra.dns" }
