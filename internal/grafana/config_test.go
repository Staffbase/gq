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
	"testing"
)

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	data := `{"url":"https://grafana.example.com","token":"glsa_abc123","logs_datasource_uid":"my-logs","metrics_datasource_uid":"my-metrics"}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != "https://grafana.example.com" {
		t.Errorf("unexpected url: %q", cfg.URL)
	}
	if cfg.Token != "glsa_abc123" {
		t.Errorf("unexpected token: %q", cfg.Token)
	}
}

func TestLoadConfig_MissingURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"token":"x"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestLoadConfig_MissingToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"url":"https://x.com"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestLoadConfig_MissingLogsUID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"url":"https://x.com","token":"t","metrics_datasource_uid":"m"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing logs_datasource_uid")
	}
}

func TestLoadConfig_MissingMetricsUID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"url":"https://x.com","token":"t","logs_datasource_uid":"l"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing metrics_datasource_uid")
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`not json`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestNewClientFromConfig_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	data := `{"url":"https://grafana.example.com","token":"glsa_abc","logs_datasource_uid":"my-logs","metrics_datasource_uid":"my-metrics"}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	c, err := NewClientFromConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BaseURL != "https://grafana.example.com" {
		t.Errorf("unexpected BaseURL: %q", c.BaseURL)
	}
	if c.Token != "glsa_abc" {
		t.Errorf("unexpected Token: %q", c.Token)
	}
}

func TestNewClientFromEnv_ConfigFilePrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	data := `{"url":"https://from-config.example.com","token":"config_token","logs_datasource_uid":"my-logs","metrics_datasource_uid":"my-metrics"}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GRAFANA_CONFIG", path)
	t.Setenv("GRAFANA_URL", "https://from-env.example.com")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "env_token")

	c, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Config file should take precedence
	if c.BaseURL != "https://from-config.example.com" {
		t.Errorf("expected config file URL, got %q", c.BaseURL)
	}
	if c.Token != "config_token" {
		t.Errorf("expected config file token, got %q", c.Token)
	}
}

func TestNewClientFromEnv_BadConfigFile(t *testing.T) {
	t.Setenv("GRAFANA_CONFIG", "/nonexistent/file.json")
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_COOKIE", "")
	t.Setenv("GRAFANA_SERVICE_ACCOUNT_TOKEN", "")

	_, err := NewClientFromEnv()
	if err == nil {
		t.Fatal("expected error for bad config file path")
	}
}
