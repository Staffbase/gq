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

	"github.com/Staffbase/gq/internal/grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func callTool(t *testing.T, q *fakeQuerier, toolName string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	s := server.NewMCPServer("test", "0.0.0")
	registerTools(s, func(_ map[string]any) (grafana.Querier, error) { return q, nil })

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

// -- traces --

const searchFixture = `{"traces":[{"traceID":"abc","rootServiceName":"web","rootTraceName":"GET /","startTimeUnixNano":"1700000000000000000","durationMs":5}]}`

// IDs are base64 as in Tempo's OTLP JSON: "AQID" = 010203, "BAUG" = 040506.
const traceFixture = `{"trace":{"resourceSpans":[
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},"scopeSpans":[{"spans":[
 {"traceId":"AQID","spanId":"AQID","parentSpanId":"","name":"GET /","startTimeUnixNano":"1000000000","endTimeUnixNano":"6000000000","status":{}}]}]},
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"db"}}]},"scopeSpans":[{"spans":[
 {"traceId":"AQID","spanId":"BAUG","parentSpanId":"AQID","name":"SELECT","startTimeUnixNano":"2000000000","endTimeUnixNano":"4000000000","status":{"code":"STATUS_CODE_ERROR"}}]}]}]}}`

func TestQueryTracesTool_Hits(t *testing.T) {
	q := &fakeQuerier{tracesResult: []byte(searchFixture)}
	result := callTool(t, q, "query_traces", map[string]any{"query": `{ status = error }`})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	var got []grafana.TraceHit
	if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RootService != "web" || got[0].RootOperation != "GET /" || got[0].DurationMs != 5 {
		t.Errorf("unexpected hits: %+v", got)
	}
}

func TestQueryTraceByIDTool_Summary(t *testing.T) {
	q := &fakeQuerier{tracesResult: []byte(traceFixture)}
	result := callTool(t, q, "query_trace_by_id", map[string]any{"trace_id": "010203"})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	var s grafana.TraceSummary
	if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &s); err != nil {
		t.Fatal(err)
	}
	if s.TraceID != "010203" || s.RootService != "web" || s.DurationMs != 5000 || s.ErrorSpans != 1 ||
		len(s.ServiceMap) != 1 || s.ServiceMap[0].From != "web" || s.ServiceMap[0].To != "db" {
		t.Errorf("unexpected summary: %+v", s)
	}
	if len(s.Spans) != 2 || s.Spans[1].ParentID != "010203" || s.Spans[1].SpanID != "040506" || !s.Spans[1].Error {
		t.Errorf("unexpected spans: %+v", s.Spans)
	}
}

func TestQueryTracesTool_RequiresQuery(t *testing.T) {
	result := callTool(t, &fakeQuerier{}, "query_traces", map[string]any{})
	if !result.IsError {
		t.Fatal("expected isError=true")
	}
}

// -- profiles --

// Nodes are [offset delta, total, self, name index]; root has total 100, self 10.
const profileFixture = `{"flamegraph":{"names":["total","main","work","main"],"total":"100",
"levels":[{"values":["0","100","10","0"]},{"values":["0","90","30","1","0","0","0","0"]},{"values":["0","60","60","2"]}]}}`

func TestQueryProfileTool_TopFunctions(t *testing.T) {
	q := &fakeQuerier{profileResult: []byte(profileFixture)}
	result := callTool(t, q, "query_profile", map[string]any{"selector": `{service_name="x"}`, "limit": float64(2)})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	var got grafana.ProfileSummary
	if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 100 || len(got.TopFunctions) != 2 ||
		got.TopFunctions[0].Name != "work" || got.TopFunctions[0].SelfPct != 60 ||
		got.TopFunctions[1].Name != "main" || got.TopFunctions[1].Self != 30 {
		t.Errorf("unexpected summary: %+v", got)
	}
	if q.capturedQuery != `{service_name="x"}` {
		t.Errorf("selector not passed: %q", q.capturedQuery)
	}
}

func TestQueryProfileTool_EmptyProfile(t *testing.T) {
	q := &fakeQuerier{profileResult: []byte(`{"flamegraph":{"names":[],"total":"0","levels":[]}}`)}
	result := callTool(t, q, "query_profile", map[string]any{"selector": "{}"})
	if !result.IsError {
		t.Fatal("expected isError=true for empty profile")
	}
}

func TestQueryProfileTool_RequiresSelector(t *testing.T) {
	result := callTool(t, &fakeQuerier{}, "query_profile", map[string]any{})
	if !result.IsError {
		t.Fatal("expected isError=true")
	}
}

func TestQueryProfileLabelValuesTool_DefaultsToServiceName(t *testing.T) {
	q := &fakeQuerier{profileResult: []byte(`{"names":["backend"]}`)}
	callTool(t, q, "query_profile_label_values", map[string]any{})
	if q.capturedQuery != "service_name" {
		t.Errorf("label = %q", q.capturedQuery)
	}
}
