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
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Config represents a stored single-instance Grafana connection configuration.
type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	// TokenCommand is an optional shell command run via sh -c on 401 to obtain
	// a fresh token. Its stdout (trimmed) replaces the current token.
	// {url} in the command is substituted with the instance URL before execution.
	TokenCommand string `json:"token_command,omitempty"`
	// LogsDatasourceUID is the Grafana datasource UID for VictoriaLogs.
	// Find it in Grafana under Administration → Data Sources → <datasource> → UID.
	LogsDatasourceUID string `json:"logs_datasource_uid"`
	// MetricsDatasourceUID is the Grafana datasource UID for VictoriaMetrics.
	// Find it in Grafana under Administration → Data Sources → <datasource> → UID.
	MetricsDatasourceUID string `json:"metrics_datasource_uid"`
}

// InstanceConfig holds per-instance settings in a registry file.
// Token is optional when TokenCommand is set (at the registry or instance level).
type InstanceConfig struct {
	URL                  string `json:"url"`
	Token                string `json:"token,omitempty"`
	TokenCommand         string `json:"token_command,omitempty"`
	LogsDatasourceUID    string `json:"logs_datasource_uid"`
	MetricsDatasourceUID string `json:"metrics_datasource_uid"`
}

// Registry is the multi-instance config format.
// Detected when the config file contains an "instances" key.
type Registry struct {
	// TokenCommand is the default token refresh command for all instances.
	// Executed via sh -c on 401; stdout (trimmed) becomes the new token.
	// The literal string {url} is replaced with the instance URL before execution.
	// Example: "vault read -field=token secret/grafana/{url}"
	// Per-instance token_command overrides this value.
	TokenCommand string                    `json:"token_command,omitempty"`
	Instances    map[string]InstanceConfig `json:"instances"`
}

// ResolvedInstance returns a fully resolved InstanceConfig for the given name,
// applying the registry-level token_command as default if the instance doesn't
// have its own.
func (r *Registry) ResolvedInstance(name string) (InstanceConfig, error) {
	inst, ok := r.Instances[name]
	if !ok {
		return InstanceConfig{}, fmt.Errorf("instance %q not found in registry (available: %s)", name, strings.Join(r.InstanceNames(), ", "))
	}
	if inst.TokenCommand == "" && r.TokenCommand != "" {
		inst.TokenCommand = strings.ReplaceAll(r.TokenCommand, "{url}", inst.URL)
	}
	return inst, nil
}

// InstanceNames returns a sorted list of instance names in the registry.
func (r *Registry) InstanceNames() []string {
	names := make([]string, 0, len(r.Instances))
	for k := range r.Instances {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// LoadConfigFileFromEnv reads GRAFANA_CONFIG and auto-detects format.
// Returns (registry, nil, nil) for registry format,
// (nil, config, nil) for single-instance format.
func LoadConfigFileFromEnv() (*Registry, *Config, error) {
	path := os.Getenv("GRAFANA_CONFIG")
	if path == "" {
		return nil, nil, fmt.Errorf("GRAFANA_CONFIG is not set")
	}
	return LoadConfigFile(path)
}

// rawFile is used to detect format before full parsing.
type rawFile struct {
	Instances json.RawMessage `json:"instances"`
	URL       string          `json:"url"`
}

// LoadConfig reads a single-instance Config from the given JSON file path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("config file %s: url is required", path)
	}
	if cfg.Token == "" && cfg.TokenCommand == "" {
		return nil, fmt.Errorf("config file %s: token or token_command is required", path)
	}
	if cfg.LogsDatasourceUID == "" {
		return nil, fmt.Errorf("config file %s: logs_datasource_uid is required (find it in Grafana under Administration → Data Sources)", path)
	}
	if cfg.MetricsDatasourceUID == "" {
		return nil, fmt.Errorf("config file %s: metrics_datasource_uid is required (find it in Grafana under Administration → Data Sources)", path)
	}
	return &cfg, nil
}

// LoadRegistry reads a multi-instance Registry from the given JSON file path.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading registry file %s: %w", path, err)
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parsing registry file %s: %w", path, err)
	}
	if len(reg.Instances) == 0 {
		return nil, fmt.Errorf("registry file %s: instances map is empty or missing", path)
	}
	for name, inst := range reg.Instances {
		if inst.URL == "" {
			return nil, fmt.Errorf("registry file %s: instance %q: url is required", path, name)
		}
		if inst.Token == "" && inst.TokenCommand == "" && reg.TokenCommand == "" {
			return nil, fmt.Errorf("registry file %s: instance %q: token or token_command is required", path, name)
		}
		if inst.LogsDatasourceUID == "" {
			return nil, fmt.Errorf("registry file %s: instance %q: logs_datasource_uid is required", path, name)
		}
		if inst.MetricsDatasourceUID == "" {
			return nil, fmt.Errorf("registry file %s: instance %q: metrics_datasource_uid is required", path, name)
		}
	}
	return &reg, nil
}

// LoadConfigFile auto-detects format: registry if "instances" key is present,
// single-instance otherwise.
func LoadConfigFile(path string) (registry *Registry, single *Config, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading config file %s: %w", path, err)
	}
	var probe rawFile
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	if probe.Instances != nil {
		reg, err := LoadRegistry(path)
		return reg, nil, err
	}
	cfg, err := LoadConfig(path)
	return nil, cfg, err
}

// NewClientFromConfig loads a single-instance config file and returns a Client.
// Returns an error if the file is a registry — use LoadRegistry + NewClientFromInstance instead.
func NewClientFromConfig(path string) (*Client, error) {
	reg, cfg, err := LoadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if reg != nil {
		return nil, fmt.Errorf("config file %s is a registry; use --instance <name> to select an instance (available: %s)", path, strings.Join(reg.InstanceNames(), ", "))
	}
	return &Client{
		BaseURL:              strings.TrimRight(cfg.URL, "/"),
		Token:                cfg.Token,
		TokenCommand:         cfg.TokenCommand,
		LogsDatasourceUID:    cfg.LogsDatasourceUID,
		MetricsDatasourceUID: cfg.MetricsDatasourceUID,
	}, nil
}

// NewClientFromInstance builds a Client from a resolved InstanceConfig.
func NewClientFromInstance(inst InstanceConfig) *Client {
	return &Client{
		BaseURL:              strings.TrimRight(inst.URL, "/"),
		Token:                inst.Token,
		TokenCommand:         inst.TokenCommand,
		LogsDatasourceUID:    inst.LogsDatasourceUID,
		MetricsDatasourceUID: inst.MetricsDatasourceUID,
	}
}
