// Copyright 2026 Staffbase GmbH.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// SPDX-License-Identifier: Apache-2.0

package grafana

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig drops body into a temp file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

const twoInstances = `{
  "instances": {
    "prod": {
      "url": "https://prod.example.com",
      "token": "prod-token",
      "logs_datasource_uid": "prod-logs",
      "metrics_datasource_uid": "prod-metrics"
    },
    "dev": {
      "url": "https://dev.example.com",
      "token": "dev-token",
      "logs_datasource_uid": "dev-logs",
      "metrics_datasource_uid": "dev-metrics"
    }
  }
}`

func TestLoadConfigFile_DetectsRegistry(t *testing.T) {
	reg, single, err := LoadConfigFile(writeConfig(t, twoInstances))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if single != nil {
		t.Error("a file with an instances key must not parse as single-instance")
	}
	if reg == nil {
		t.Fatal("expected a registry")
	}
	if got := strings.Join(reg.InstanceNames(), ","); got != "dev,prod" {
		t.Errorf("expected instance names sorted, got %q", got)
	}
}

func TestLoadConfigFile_DetectsSingleInstance(t *testing.T) {
	body := `{"url":"https://x.example.com","token":"t","logs_datasource_uid":"l","metrics_datasource_uid":"m"}`
	reg, single, err := LoadConfigFile(writeConfig(t, body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg != nil {
		t.Error("a file without an instances key must not parse as a registry")
	}
	if single == nil || single.URL != "https://x.example.com" {
		t.Fatalf("expected the single-instance config, got %+v", single)
	}
}

func TestRegistry_ResolvedInstance(t *testing.T) {
	reg, _, err := LoadConfigFile(writeConfig(t, twoInstances))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.ResolvedInstance("prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.URL != "https://prod.example.com" || inst.Token != "prod-token" {
		t.Errorf("resolved the wrong instance: %+v", inst)
	}
}

func TestRegistry_UnknownInstanceListsTheKnownOnes(t *testing.T) {
	reg, _, err := LoadConfigFile(writeConfig(t, twoInstances))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.ResolvedInstance("staging")
	if err == nil {
		t.Fatal("expected an error for an unknown instance")
	}
	// The list is the whole point: a typo should not require reading the config.
	for _, want := range []string{"staging", "dev", "prod"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in error, got: %v", want, err)
		}
	}
}

// {url} stays a placeholder here on purpose — Client expands it when it runs
// the command. See TestTokenCommand_SubstitutesURL.
func TestRegistry_TokenCommandDefaultsToTheRegistryLevelOne(t *testing.T) {
	body := `{
	  "token_command": "mint --for {url}",
	  "instances": {
	    "prod": {"url":"https://prod.example.com","logs_datasource_uid":"l","metrics_datasource_uid":"m"},
	    "dev":  {"url":"https://dev.example.com","logs_datasource_uid":"l","metrics_datasource_uid":"m",
	             "token_command":"mint-dev"}
	  }
	}`
	reg, _, err := LoadConfigFile(writeConfig(t, body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prod, err := reg.ResolvedInstance("prod")
	if err != nil {
		t.Fatal(err)
	}
	if prod.TokenCommand != "mint --for {url}" {
		t.Errorf("expected the registry-level command inherited verbatim, got %q", prod.TokenCommand)
	}

	dev, err := reg.ResolvedInstance("dev")
	if err != nil {
		t.Fatal(err)
	}
	if dev.TokenCommand != "mint-dev" {
		t.Errorf("expected the per-instance command to win, got %q", dev.TokenCommand)
	}
}

// A registry-level token_command is what makes a token-less instance valid.
func TestLoadRegistry_TokenlessInstanceNeedsSomeTokenCommand(t *testing.T) {
	withoutCommand := `{"instances":{"prod":{"url":"https://x","logs_datasource_uid":"l","metrics_datasource_uid":"m"}}}`
	if _, err := LoadRegistry(writeConfig(t, withoutCommand)); err == nil {
		t.Error("expected an error for an instance with neither token nor token_command")
	}

	withCommand := `{"token_command":"mint","instances":{"prod":{"url":"https://x","logs_datasource_uid":"l","metrics_datasource_uid":"m"}}}`
	if _, err := LoadRegistry(writeConfig(t, withCommand)); err != nil {
		t.Errorf("a registry-level token_command should satisfy the check, got: %v", err)
	}
}

func TestLoadRegistry_RejectsIncompleteInstances(t *testing.T) {
	cases := map[string]string{
		"missing url":          `{"instances":{"p":{"token":"t","logs_datasource_uid":"l","metrics_datasource_uid":"m"}}}`,
		"missing logs uid":     `{"instances":{"p":{"url":"https://x","token":"t","metrics_datasource_uid":"m"}}}`,
		"missing metrics uid":  `{"instances":{"p":{"url":"https://x","token":"t","logs_datasource_uid":"l"}}}`,
		"empty instances map":  `{"instances":{}}`,
		"instances wrong type": `{"instances":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadRegistry(writeConfig(t, body)); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

// NewClientFromConfig cannot pick an instance for you; it must say so rather
// than guess or report a confusing missing-url error.
func TestNewClientFromConfig_RejectsRegistryWithGuidance(t *testing.T) {
	_, err := NewClientFromConfig(writeConfig(t, twoInstances))
	if err == nil {
		t.Fatal("expected an error when pointing NewClientFromConfig at a registry")
	}
	for _, want := range []string{"--instance", "dev", "prod"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in error, got: %v", want, err)
		}
	}
}

func TestNewClientFromInstance_CarriesTokenCommand(t *testing.T) {
	c := NewClientFromInstance(InstanceConfig{
		URL:                  "https://x.example.com/",
		Token:                "t",
		TokenCommand:         "mint",
		LogsDatasourceUID:    "l",
		MetricsDatasourceUID: "m",
	})
	if c.BaseURL != "https://x.example.com" {
		t.Errorf("expected the trailing slash trimmed, got %q", c.BaseURL)
	}
	if c.TokenCommand != "mint" {
		t.Errorf("expected token_command carried onto the client, got %q", c.TokenCommand)
	}
}
