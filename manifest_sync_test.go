package manifest_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestEmbeddedPluginManifestMatchesRoot(t *testing.T) {
	root := readJSONFile(t, "plugin.json")
	embedded := readJSONFile(t, "cmd/workflow-plugin-namecheap/plugin.json")
	if !reflect.DeepEqual(root, embedded) {
		t.Fatal("embedded plugin manifest must match root plugin.json")
	}
}

func TestManifestDeclaresIaCProviderCapability(t *testing.T) {
	manifest := readPluginManifest(t, "plugin.json")
	assertIaCProviderCapability(t, manifest, "namecheap", []string{"infra.dns", "infra.domain_transfer", "infra.dns_delegation"})
}

func TestManifestClientIPSetupContract(t *testing.T) {
	manifest := readPluginManifest(t, "plugin.json")
	for _, secret := range manifest.RequiredSecrets {
		if secret.Name == "NAMECHEAP_CLIENT_IP" {
			t.Fatal("NAMECHEAP_CLIENT_IP must remain non-secret configuration")
		}
	}
	count := 0
	for _, config := range manifest.RequiredConfig {
		if config.Name != "NAMECHEAP_CLIENT_IP" {
			continue
		}
		count++
		if config.Key != "client_ip" || config.Sensitive {
			t.Errorf("client IP config must preserve the non-secret client_ip key: %#v", config)
		}
		for _, part := range []string{"ClientIp", "every API request", "already", "does not update the allowlist"} {
			if !strings.Contains(config.Description, part) {
				t.Errorf("client IP description must explain %q: %q", part, config.Description)
			}
		}
		for _, part := range []string{"Runner", "IPv4", "already allowlisted"} {
			if !strings.Contains(config.Prompt, part) {
				t.Errorf("client IP prompt must identify %q: %q", part, config.Prompt)
			}
		}
	}
	if count != 1 {
		t.Errorf("got %d NAMECHEAP_CLIENT_IP config entries, want one", count)
	}
}

type setupValue struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	Sensitive   bool   `json:"sensitive"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

type pluginManifest struct {
	RequiredConfig  []setupValue `json:"required_config"`
	RequiredSecrets []setupValue `json:"required_secrets"`
	Capabilities    struct {
		ResourceTypes []string `json:"resourceTypes"`
		IaCProvider   struct {
			Name          string   `json:"name"`
			ResourceTypes []string `json:"resourceTypes"`
		} `json:"iacProvider"`
	} `json:"capabilities"`
}

func assertIaCProviderCapability(t *testing.T, manifest pluginManifest, name string, resourceTypes []string) {
	t.Helper()
	if manifest.Capabilities.IaCProvider.Name != name {
		t.Fatalf("capabilities.iacProvider.name = %q, want %q", manifest.Capabilities.IaCProvider.Name, name)
	}
	if !slices.Equal(manifest.Capabilities.IaCProvider.ResourceTypes, resourceTypes) {
		t.Fatalf("capabilities.iacProvider.resourceTypes = %#v, want %#v", manifest.Capabilities.IaCProvider.ResourceTypes, resourceTypes)
	}
	if !slices.Equal(manifest.Capabilities.ResourceTypes, resourceTypes) {
		t.Fatalf("capabilities.resourceTypes = %#v, want %#v", manifest.Capabilities.ResourceTypes, resourceTypes)
	}
}

func readJSONFile(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return value
}

func readPluginManifest(t *testing.T, path string) pluginManifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value pluginManifest
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return value
}
