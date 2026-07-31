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

// -- clientCache / the instance tool parameter --

func newTestCache(t *testing.T) *clientCache {
	t.Helper()
	setConfig(t, registryJSON)
	reg, err := registryFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if reg == nil {
		t.Fatal("expected a registry")
	}
	return newClientCache(reg)
}

func TestClientCache_ReturnsTheSameClientPerInstance(t *testing.T) {
	cc := newTestCache(t)
	first, err := cc.querierFor(map[string]any{"instance": "prod"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := cc.querierFor(map[string]any{"instance": "prod"})
	if err != nil {
		t.Fatal(err)
	}
	// Identity matters: a fresh client per call would throw away a token that
	// token_command just refreshed, so every call would pay the 401 again.
	if first != second {
		t.Error("expected the cached client to be reused across calls")
	}

	other, err := cc.querierFor(map[string]any{"instance": "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Error("expected a distinct client per instance")
	}
}

func TestClientCache_MissingInstanceListsTheOptions(t *testing.T) {
	cc := newTestCache(t)
	for _, args := range []map[string]any{
		{},
		{"instance": ""},
		{"instance": 42},
	} {
		_, err := cc.querierFor(args)
		if err == nil {
			t.Fatalf("expected an error for args %v", args)
		}
		for _, want := range []string{"dev", "prod"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("expected %q in error for args %v, got: %v", want, args, err)
			}
		}
	}
}

func TestClientCache_UnknownInstanceIsAToolError(t *testing.T) {
	cc := newTestCache(t)
	if _, err := cc.querierFor(map[string]any{"instance": "staging"}); err == nil {
		t.Fatal("expected an error for an unknown instance")
	}
}

// In registry mode every tool must advertise the instance parameter, or an agent
// has no way to know it is required until the call fails.
func TestRegistryMode_AllToolsRequireInstance(t *testing.T) {
	cc := newTestCache(t)
	s := server.NewMCPServer("test", "0.0.0")
	registerTools(s, cc.querierFor,
		mcp.WithString("instance", mcp.Required(), mcp.Description("Grafana instance name.")))

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
	cc := newTestCache(t)
	s := server.NewMCPServer("test", "0.0.0")
	registerTools(s, cc.querierFor,
		mcp.WithString("instance", mcp.Required(), mcp.Description("Grafana instance name.")))

	tool := s.GetTool("query_logs")
	req := mcp.CallToolRequest{}
	req.Params.Name = "query_logs"
	req.Params.Arguments = map[string]any{"query": "severity:ERROR"}

	result, err := tool.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("expected a tool error, got a protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError to be set")
	}
}

// Single-instance mode must not grow an instance parameter.
func TestSingleInstanceMode_HasNoInstanceParameter(t *testing.T) {
	s := server.NewMCPServer("test", "0.0.0")
	q := &fakeQuerier{}
	registerTools(s, func(map[string]any) (grafana.Querier, error) { return q, nil })

	tool := s.GetTool("query_logs")
	if _, ok := tool.Tool.InputSchema.Properties["instance"]; ok {
		t.Error("single-instance mode should not advertise an instance parameter")
	}
}
