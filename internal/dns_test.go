package internal

import (
	"errors"
	"strings"
	"testing"
)

func TestConfig_Validate_HappyPath(t *testing.T) {
	cfg := Config{APIUser: "alice", APIKey: "k", ClientIP: "203.0.113.10"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid config: %v", err)
	}
}

func TestConfig_Validate_RequiresAllFields(t *testing.T) {
	cases := map[string]Config{
		"no api_user":  {APIKey: "k", ClientIP: "1.1.1.1"},
		"no api_key":   {APIUser: "u", ClientIP: "1.1.1.1"},
		"no client_ip": {APIUser: "u", APIKey: "k"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrAuthMissing) {
				t.Errorf("err = %v; want wrapped ErrAuthMissing", err)
			}
		})
	}
}

func TestConfig_Validate_RejectsCIDR(t *testing.T) {
	cfg := Config{APIUser: "u", APIKey: "k", ClientIP: "10.0.0.0/8"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for CIDR client_ip")
	}
	if !strings.Contains(err.Error(), "valid IP") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestNewDriver_PropagatesValidationErr(t *testing.T) {
	_, err := NewDriver(Config{APIUser: "u"})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestDriver_Type(t *testing.T) {
	d, _ := NewDriver(Config{APIUser: "u", APIKey: "k", ClientIP: "1.1.1.1"})
	if d.Type() != "infra.dns" {
		t.Errorf("Type = %q want infra.dns", d.Type())
	}
}

func TestDriver_Configure_ResetsClient(t *testing.T) {
	d, _ := NewDriver(Config{APIUser: "u", APIKey: "k", ClientIP: "1.1.1.1"})
	d.client = "stub"
	if err := d.Configure(nil, Config{APIUser: "u2", APIKey: "k2", ClientIP: "2.2.2.2"}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if d.client != nil {
		t.Errorf("client should reset to nil; got %v", d.client)
	}
	if d.cfg.APIUser != "u2" {
		t.Errorf("cfg not updated; got %v", d.cfg)
	}
}

func TestConfig_Sandbox_DefaultFalse(t *testing.T) {
	cfg := Config{APIUser: "u", APIKey: "k", ClientIP: "1.2.3.4"}
	if cfg.Sandbox {
		t.Error("Sandbox should default to false")
	}
}

func TestConfig_Sandbox_CanBeEnabled(t *testing.T) {
	cfg := Config{APIUser: "u", APIKey: "k", ClientIP: "1.2.3.4", Sandbox: true}
	if err := cfg.Validate(); err != nil {
		t.Errorf("sandbox config: %v", err)
	}
	if !cfg.Sandbox {
		t.Error("Sandbox should be true")
	}
}

func TestDNSRecord_Types(t *testing.T) {
	// Verify DNSRecord struct can hold all supported record types.
	supported := []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "CAA"}
	for _, rtype := range supported {
		r := DNSRecord{Type: rtype, Name: "@", Data: "example.com", TTL: 1800}
		if r.Type != rtype {
			t.Errorf("DNSRecord.Type = %q, want %q", r.Type, rtype)
		}
	}
}

func TestDNSSpec_Struct(t *testing.T) {
	spec := DNSSpec{
		Domain: "example.com",
		Records: []DNSRecord{
			{Type: "A", Name: "@", Data: "1.2.3.4", TTL: 1800},
		},
	}
	if spec.Domain != "example.com" {
		t.Errorf("Domain = %q, want example.com", spec.Domain)
	}
	if len(spec.Records) != 1 {
		t.Errorf("Records len = %d, want 1", len(spec.Records))
	}
}
