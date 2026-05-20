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
