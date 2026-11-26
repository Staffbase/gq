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

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Staffbase/gq/internal/grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const registryJSON = `{
  "instances": {
    "prod": {"url":"https://prod.example.com","token":"p","logs_datasource_uid":"l","metrics_datasource_uid":"m"},
    "dev":  {"url":"https://dev.example.com","token":"d","logs_datasource_uid":"l","metrics_datasource_uid":"m"}
  }
}`

// setConfig points GRAFANA_CONFIG at a temp file holding body.
func setConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAFANA_CONFIG", path)
	return path
}

// -- registryFromEnv --

func TestRegistryFromEnv_NoConfigIsNotAnError(t *testing.T) {
	t.Setenv("GRAFANA_CONFIG", "")
	reg, err := registryFromEnv()
	if err != nil {
		t.Fatalf("an unset GRAFANA_CONFIG should fall through to env vars, got: %v", err)
	}
	if reg != nil {
		t.Error("expected no registry")
	}
}

func TestRegistryFromEnv_SingleInstanceYieldsNoRegistry(t *testing.T) {
	setConfig(t, `{"url":"https://x","token":"t","logs_datasource_uid":"l","metrics_datasource_uid":"m"}`)
	reg, err := registryFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg != nil {
		t.Error("a single-instance config must not be treated as a registry")
	}
}

// The regression this guards: a GRAFANA_CONFIG that is set but broken used to
// fall through to single-instance mode, so the server died complaining that
// GRAFANA_URL was missing — pointing at the wrong problem entirely.
func TestRegistryFromEnv_BrokenConfigIsFatal(t *testing.T) {
	cases := map[string]string{
		"malformed json":   `{"instances": {`,
		"invalid registry": `{"instances":{"prod":{"url":"https://x"}}}`,
		"invalid single":   `{"url":"https://x"}`,
		"empty instances":  `{"instances":{}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := setConfig(t, body)
			_, err := registryFromEnv()
			if err == nil {
				t.Fatal("expected a broken config file to be fatal")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the error should name the offending file, got: %v", err)
			}
			if strings.Contains(err.Error(), "GRAFANA_URL") {
				t.Errorf("the error should not blame env vars, got: %v", err)
			}
		})
	}
}

func TestRegistryFromEnv_MissingFileIsFatal(t *testing.T) {
	t.Setenv("GRAFANA_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.json"))
	if _, err := registryFromEnv(); err == nil {
		t.Fatal("expected a missing config file to be fatal")
	}
}

// -- the instance tool parameter --

func testRegistryInstances(t *testing.T) grafana.Instances {
	t.Helper()
	setConfig(t, registryJSON)
	reg, err := registryFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if reg == nil {
		t.Fatal("expected a registry")
	}
	return grafana.RegistryInstances(reg, grafana.DefaultUserAgent)
}

// In registry mode every tool must advertise the instance parameter, or an agent
// has no way to know it is required until the call fails.
func TestRegistryMode_AllToolsRequireInstance(t *testing.T) {
	s := server.NewMCPServer("test", "0.0.0")
	registerTools(s, testRegistryInstances(t))

	for _, name := range []string{"query_logs", "query_metrics", "query_metrics_instant", "list_label_values"} {
		tool := s.GetTool(name)
		if tool == nil {
			t.Fatalf("tool %q not registered", name)
		}
		if _, ok := tool.Tool.InputSchema.Properties["instance"]; !ok {
			t.Errorf("tool %q does not declare an instance parameter", name)
		}
		if !slices.Contains(tool.Tool.InputSchema.Required, "instance") {
			t.Errorf("tool %q does not mark instance as required", name)
		}
		// The original parameters must survive being appended to.
		if _, ok := tool.Tool.InputSchema.Properties["query"]; !ok && name != "list_label_values" {
			t.Errorf("tool %q lost its query parameter", name)
		}
	}
}

// A tool call that omits instance must come back as a tool error the agent can
// read, not a protocol error that kills the session.
func TestRegistryMode_MissingInstanceIsAReadableToolError(t *testing.T) {
	s := server.NewMCPServer("test", "0.0.0")
	registerTools(s, testRegistryInstances(t))

	tool := s.GetTool("query_logs")
	req := mcp.CallToolRequest{}
	req.Params.Name = "query_logs"
	for _, args := range []map[string]any{
		{"query": "severity:ERROR"},
		{"query": "severity:ERROR", "instance": 42},
	} {
		req.Params.Arguments = args
		result, err := tool.Handler(context.Background(), req)
		if err != nil {
			t.Fatalf("expected a tool error, got a protocol error: %v", err)
		}
		if !result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "dev, prod") {
			t.Errorf("args %v: want a tool error listing the instances, got %+v", args, result)
		}
	}
}

// Single-instance mode must not grow an instance parameter.
func TestSingleInstanceMode_HasNoInstanceParameter(t *testing.T) {
	s := server.NewMCPServer("test", "0.0.0")
	q := &fakeQuerier{}
	registerTools(s, grafana.SingleInstance(q))

	tool := s.GetTool("query_logs")
	if _, ok := tool.Tool.InputSchema.Properties["instance"]; ok {
		t.Error("single-instance mode should not advertise an instance parameter")
	}
}
