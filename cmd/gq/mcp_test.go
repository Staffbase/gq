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
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func callTool(t *testing.T, q *fakeQuerier, toolName string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	s := server.NewMCPServer("test", "0.0.0")
	registerTools(s, q)

	tool := s.GetTool(toolName)
	if tool == nil {
		t.Fatalf("tool %q not registered", toolName)
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args

	result, err := tool.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned unexpected protocol error: %v", err)
	}
	return result
}

// -- query_logs --

func TestQueryLogsTool_Success(t *testing.T) {
	q := &fakeQuerier{logsResult: []byte(`{"_msg":"hello","severity":"ERROR"}`)}
	result := callTool(t, q, "query_logs", map[string]any{"query": "severity:ERROR _time:1h", "limit": float64(10)})
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "hello") {
		t.Errorf("expected log content, got: %q", text)
	}
	if q.capturedLimit != 10 {
		t.Errorf("expected limit 10, got %d", q.capturedLimit)
	}
}

func TestQueryLogsTool_DefaultLimit(t *testing.T) {
	q := &fakeQuerier{logsResult: []byte(`{"_msg":"hi"}`)}
	callTool(t, q, "query_logs", map[string]any{"query": "_time:5m"})
	if q.capturedLimit != 100 {
		t.Errorf("expected default limit 100, got %d", q.capturedLimit)
	}
}

func TestQueryLogsTool_ClientError(t *testing.T) {
	q := &fakeQuerier{logsErr: errors.New("HTTP 401: Unauthorized")}
	result := callTool(t, q, "query_logs", map[string]any{"query": "*"})
	if !result.IsError {
		t.Fatal("expected isError=true")
	}
	if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "401") {
		t.Errorf("expected 401 in error")
	}
}

// -- query_metrics --

func TestQueryMetricsTool_Defaults(t *testing.T) {
	q := &fakeQuerier{metricsResult: []byte(`{"status":"success"}`)}
	callTool(t, q, "query_metrics", map[string]any{"query": "up"})
	if q.capturedStart != "now-1h" {
		t.Errorf("expected default start 'now-1h', got %q", q.capturedStart)
	}
	if q.capturedStep != "60s" {
		t.Errorf("expected default step '60s', got %q", q.capturedStep)
	}
}

func TestQueryMetricsTool_ClientError(t *testing.T) {
	q := &fakeQuerier{metricsErr: errors.New("HTTP 400: bad request")}
	result := callTool(t, q, "query_metrics", map[string]any{"query": "bad{"})
	if !result.IsError {
		t.Fatal("expected isError=true")
	}
}

func TestQueryMetricsTool_PrettyJSON(t *testing.T) {
	raw := `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	q := &fakeQuerier{metricsResult: []byte(raw)}
	result := callTool(t, q, "query_metrics", map[string]any{"query": "up"})
	if result.IsError {
		t.Fatalf("unexpected error")
	}
	if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "\n") {
		t.Errorf("expected pretty-printed JSON")
	}
}

// -- query_metrics_instant --

func TestQueryMetricsInstantTool_Success(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"flink"},"value":[1234567890,"42"]}]}}`
	q := &fakeQuerier{instantResult: []byte(payload)}
	result := callTool(t, q, "query_metrics_instant", map[string]any{"query": "up"})
	if result.IsError {
		t.Fatalf("unexpected error")
	}
	if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "42") {
		t.Errorf("expected value in result")
	}
}

func TestQueryMetricsInstantTool_ClientError(t *testing.T) {
	q := &fakeQuerier{instantErr: errors.New("HTTP 500: internal server error")}
	result := callTool(t, q, "query_metrics_instant", map[string]any{"query": "up"})
	if !result.IsError {
		t.Fatal("expected isError=true")
	}
}

// -- list_label_values --

func TestListLabelValuesTool_Success(t *testing.T) {
	q := &fakeQuerier{labelValues: []string{"flink", "monitoring", "kube-system"}}
	result := callTool(t, q, "list_label_values", map[string]any{"label": "namespace"})
	if result.IsError {
		t.Fatalf("unexpected error")
	}
	var values []string
	if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &values); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
	if len(values) != 3 {
		t.Errorf("expected 3 values, got %d", len(values))
	}
}

func TestListLabelValuesTool_ClientError(t *testing.T) {
	q := &fakeQuerier{labelErr: errors.New("HTTP 403: forbidden")}
	result := callTool(t, q, "list_label_values", map[string]any{"label": "namespace"})
	if !result.IsError {
		t.Fatal("expected isError=true")
	}
	if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "403") {
		t.Errorf("expected 403 in error")
	}
}
