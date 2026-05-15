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
)

// Config represents a stored Grafana connection configuration.
type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	// LogsDatasourceUID is the Grafana datasource UID for VictoriaLogs.
	// Defaults to "victorialogs" if unset.
	LogsDatasourceUID string `json:"logs_datasource_uid,omitempty"`
	// MetricsDatasourceUID is the Grafana datasource UID for VictoriaMetrics.
	// Defaults to "victoriametrics" if unset.
	MetricsDatasourceUID string `json:"metrics_datasource_uid,omitempty"`
}

// LoadConfig reads a Config from the given JSON file path.
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
	if cfg.Token == "" {
		return nil, fmt.Errorf("config file %s: token is required", path)
	}
	return &cfg, nil
}

// NewClientFromConfig loads a config file and returns a Client configured
// with token-based auth. Datasource UIDs default to "victorialogs" and
// "victoriametrics" if not specified in the config file.
func NewClientFromConfig(path string) (*Client, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}
	logsUID := cfg.LogsDatasourceUID
	if logsUID == "" {
		logsUID = "victorialogs"
	}
	metricsUID := cfg.MetricsDatasourceUID
	if metricsUID == "" {
		metricsUID = "victoriametrics"
	}
	return &Client{
		BaseURL:              cfg.URL,
		Token:                cfg.Token,
		LogsDatasourceUID:    logsUID,
		MetricsDatasourceUID: metricsUID,
	}, nil
}
