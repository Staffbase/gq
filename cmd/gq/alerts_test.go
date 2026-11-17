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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Staffbase/gq/internal/alerts"
	"github.com/Staffbase/gq/internal/grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func fakeAlert(fp, alertname, namespace string) grafana.Alert {
	var a grafana.Alert
	a.Fingerprint = fp
	a.Status.State = "active"
	a.StartsAt = time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	a.Labels = map[string]string{"alertname": alertname, "namespace": namespace, "severity": "warning"}
	return a
}

// fakeGet serves one fakeQuerier per instance name; unknown names fail like an
// unknown registry instance does.
func fakeGet(qs map[string]*fakeQuerier) func(string) (grafana.Querier, error) {
	return func(name string) (grafana.Querier, error) {
		if q, ok := qs[name]; ok {
			return q, nil
		}
		return nil, fmt.Errorf("instance %q not found", name)
	}
}

// fakeInstances are named instances served by fakeGet, or one unnamed
// instance when names is empty.
func fakeInstances(qs map[string]*fakeQuerier, names ...string) grafana.Instances {
	if len(names) == 0 {
		names = []string{""}
	}
	return grafana.Instances{Names: names, Get: fakeGet(qs)}
}

func callAlertTool(t *testing.T, in grafana.Instances, toolName string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	s := server.NewMCPServer("test", "0.0.0")
	registerAlertTools(s, in)
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

func resultText(r *mcp.CallToolResult) string { return r.Content[0].(mcp.TextContent).Text }

// -- list_alerts --

func TestListAlertsTool_AllInstancesAtOnce(t *testing.T) {
	dev := &fakeQuerier{alerts: []grafana.Alert{fakeAlert("d1", "HighErrorRate", "shop")}}
	prod := &fakeQuerier{alerts: []grafana.Alert{fakeAlert("p1", "HighErrorRate", "cart")}}
	stage := &fakeQuerier{alertsErr: errors.New("HTTP 403: forbidden")}
	in := fakeInstances(map[string]*fakeQuerier{"dev": dev, "prod": prod, "stage": stage}, "dev", "prod", "stage")

	r := callAlertTool(t, in, "list_alerts", map[string]any{
		"matchers": []any{`alertname="HighErrorRate"`}, "state": "suppressed",
	})
	if r.IsError {
		t.Fatalf("one failing instance must not fail the call: %s", resultText(r))
	}
	var list alerts.Result
	if err := json.Unmarshal([]byte(resultText(r)), &list); err != nil {
		t.Fatalf("want JSON, got %q: %v", resultText(r), err)
	}
	if len(list.Alerts) != 2 || list.CommonLabels["alertname"] != "HighErrorRate" {
		t.Errorf("want both instances' alerts with the shared alertname hoisted, got %+v", list)
	}
	if list.Errors["stage"] != "HTTP 403: forbidden" {
		t.Errorf("want stage's error, got %v", list.Errors)
	}
	for name, q := range map[string]*fakeQuerier{"dev": dev, "prod": prod, "stage": stage} {
		if !slices.Equal(q.capturedFilter.Matchers, []string{`alertname="HighErrorRate"`}) || q.capturedFilter.State != "suppressed" {
			t.Errorf("%s: filter not passed to Grafana: %+v", name, q.capturedFilter)
		}
	}
	if strings.Contains(resultText(r), "\n") {
		t.Errorf("want compact JSON, got %q", resultText(r))
	}
}

func TestListAlertsTool_OneInstance(t *testing.T) {
	dev := &fakeQuerier{alerts: []grafana.Alert{fakeAlert("d1", "X", "shop")}}
	prod := &fakeQuerier{}
	in := fakeInstances(map[string]*fakeQuerier{"dev": dev, "prod": prod}, "dev", "prod")

	r := callAlertTool(t, in, "list_alerts", map[string]any{"instance": "dev"})
	if r.IsError || !strings.Contains(resultText(r), `"d1"`) {
		t.Fatalf("want dev's alert, got %s", resultText(r))
	}
	if prod.capturedFilter.State != "" {
		t.Error("prod must not be queried when instance is dev")
	}
}

func TestListAlertsTool_Limit(t *testing.T) {
	q := &fakeQuerier{alerts: []grafana.Alert{fakeAlert("a", "X", "s"), fakeAlert("b", "X", "s"), fakeAlert("c", "X", "s")}}
	in := fakeInstances(map[string]*fakeQuerier{"": q})

	r := callAlertTool(t, in, "list_alerts", map[string]any{"limit": float64(2)})
	if !strings.Contains(resultText(r), `"more":1`) {
		t.Errorf("want more=1 with limit 2, got %s", resultText(r))
	}
	for _, bad := range []float64{0, -1, 1.5, 1001} {
		if r := callAlertTool(t, in, "list_alerts", map[string]any{"limit": bad}); !r.IsError ||
			resultText(r) != "limit must be a positive integer not exceeding 1000" {
			t.Errorf("limit %v should be rejected with the usual message, got %q", bad, resultText(r))
		}
	}
}

func TestListAlertsTool_SingleInstanceModeHasNoInstanceArg(t *testing.T) {
	q := &fakeQuerier{alerts: []grafana.Alert{fakeAlert("a", "X", "s")}}
	in := fakeInstances(map[string]*fakeQuerier{"": q})
	s := server.NewMCPServer("test", "0.0.0")
	registerAlertTools(s, in)
	for _, name := range []string{"list_alerts", "get_alert"} {
		if _, ok := s.GetTool(name).Tool.InputSchema.Properties["instance"]; ok {
			t.Errorf("%s: no instance argument in single-instance mode", name)
		}
	}
	r := callAlertTool(t, in, "list_alerts", nil)
	if r.IsError || strings.Contains(resultText(r), `"instance"`) {
		t.Errorf("want alerts without instance names, got %s", resultText(r))
	}
}

func TestListAlertsTool_MatchersMustBeStrings(t *testing.T) {
	in := fakeInstances(map[string]*fakeQuerier{"": {}})
	for _, bad := range []any{`namespace="shop"`, []any{1}} {
		if r := callAlertTool(t, in, "list_alerts", map[string]any{"matchers": bad}); !r.IsError ||
			resultText(r) != "matchers must be an array of strings" {
			t.Errorf("matchers %v should be rejected, got %q", bad, resultText(r))
		}
	}
}

// get_alert names the query tool that runs an alert rule's query; that tool
// must exist.
func TestAlertQueryToolsAreRegistered(t *testing.T) {
	s := server.NewMCPServer("test", "0.0.0")
	registerTools(s, grafana.SingleInstance(&fakeQuerier{}))
	for kind, name := range alerts.QueryTools {
		if s.GetTool(name) == nil {
			t.Errorf("%s datasources point to %q, which is not a registered tool", kind, name)
		}
	}
}

// -- get_alert --

func TestGetAlertTool_Markdown(t *testing.T) {
	prod := &fakeQuerier{alertDetail: &grafana.AlertDetail{Alert: fakeAlert("p1", "HighErrorRate", "shop"), BaseURL: "https://grafana.example.com"}}
	in := fakeInstances(map[string]*fakeQuerier{"prod": prod}, "dev", "prod")

	r := callAlertTool(t, in, "get_alert", map[string]any{"instance": "prod", "fingerprint": "p1", "alertname": "HighErrorRate"})
	if r.IsError {
		t.Fatal(resultText(r))
	}
	if prod.capturedFingerprint != "p1" || prod.capturedAlertname != "HighErrorRate" {
		t.Errorf("fingerprint/alertname not passed: %q %q", prod.capturedFingerprint, prod.capturedAlertname)
	}
	if !strings.HasPrefix(resultText(r), "# HighErrorRate\n- instance: prod (https://grafana.example.com)\n") {
		t.Errorf("want the Markdown header with the instance, got:\n%s", resultText(r))
	}
}

func TestGetAlertTool_RequiredArgs(t *testing.T) {
	in := fakeInstances(map[string]*fakeQuerier{"dev": {}}, "dev")
	for _, args := range []map[string]any{{"fingerprint": "x"}, {"instance": "dev"}} {
		if r := callAlertTool(t, in, "get_alert", args); !r.IsError {
			t.Errorf("args %v should be rejected", args)
		}
	}
}

// -- CLI --

func TestAlertsCmd_AllRegistryInstances(t *testing.T) {
	setConfig(t, registryJSON)
	qs := map[string]*fakeQuerier{
		"dev":  {alerts: []grafana.Alert{fakeAlert("d1", "X", "shop")}},
		"prod": {alerts: []grafana.Alert{fakeAlert("p1", "X", "cart")}},
	}
	withFakeInstances(t, qs)

	var out bytes.Buffer
	cmd := buildRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"alerts", "-m", `namespace=~"shop|cart"`, "-m", `team="t"`, "--state", "all"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"d1"`) || !strings.Contains(out.String(), `"p1"`) {
		t.Errorf("want alerts of dev and prod, got %s", out.String())
	}
	for name, q := range qs {
		if !slices.Equal(q.capturedFilter.Matchers, []string{`namespace=~"shop|cart"`, `team="t"`}) || q.capturedFilter.State != "all" {
			t.Errorf("%s: filter = %+v", name, q.capturedFilter)
		}
	}
}

func TestAlertCmd_PrintsMarkdown(t *testing.T) {
	setConfig(t, registryJSON)
	prod := &fakeQuerier{alertDetail: &grafana.AlertDetail{Alert: fakeAlert("p1", "X", "shop"), BaseURL: "https://prod.example.com"}}
	withFakeInstances(t, map[string]*fakeQuerier{"prod": prod})

	var out bytes.Buffer
	cmd := buildRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"alert", "p1", "--instance", "prod", "--alertname", "X"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "# X\n- instance: prod (https://prod.example.com)\n") || prod.capturedAlertname != "X" {
		t.Errorf("unexpected output:\n%s", out.String())
	}
}

// withFakeInstances makes newQuerier serve one fake per instance name.
func withFakeInstances(t *testing.T, qs map[string]*fakeQuerier) {
	t.Helper()
	orig := newQuerier
	newQuerier = fakeGet(qs)
	t.Cleanup(func() { newQuerier = orig })
}
