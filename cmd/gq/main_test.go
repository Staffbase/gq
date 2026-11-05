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
	"errors"
	"testing"

	"github.com/Staffbase/gq/internal/grafana"
)

type fakeQuerier struct {
	logsResult    []byte
	logsErr       error
	metricsResult []byte
	metricsErr    error
	instantResult []byte
	instantErr    error
	labelValues   []string
	labelErr      error
	hits          []grafana.TraceHit
	trace         *grafana.TraceSummary
	tracesErr     error
	profileTypes  []grafana.ProfileType
	profileValues []string
	profile       *grafana.ProfileSummary
	profileErr    error

	capturedQuery string
	capturedStart string
	capturedEnd   string
	capturedStep  string
	capturedLimit int
	capturedTime  string
	capturedType  string
}

func (f *fakeQuerier) QueryLogs(query, start, end string, limit int) ([]byte, error) {
	f.capturedQuery = query
	f.capturedStart = start
	f.capturedEnd = end
	f.capturedLimit = limit
	return f.logsResult, f.logsErr
}

func (f *fakeQuerier) QueryMetricsRange(query, start, end, step string) ([]byte, error) {
	f.capturedQuery = query
	f.capturedStart = start
	f.capturedEnd = end
	f.capturedStep = step
	return f.metricsResult, f.metricsErr
}

func (f *fakeQuerier) QueryMetricsInstant(query, t string) ([]byte, error) {
	f.capturedQuery = query
	f.capturedTime = t
	return f.instantResult, f.instantErr
}

func (f *fakeQuerier) ListLabelValues(label, match string) ([]string, error) {
	return f.labelValues, f.labelErr
}

func (f *fakeQuerier) SearchTraces(query, start, end string, limit int) ([]grafana.TraceHit, error) {
	f.capturedQuery = query
	f.capturedStart = start
	f.capturedLimit = limit
	return f.hits, f.tracesErr
}

func (f *fakeQuerier) GetTrace(traceID string) (*grafana.TraceSummary, error) {
	f.capturedQuery = traceID
	return f.trace, f.tracesErr
}

func (f *fakeQuerier) ProfileTypes() ([]grafana.ProfileType, error) {
	return f.profileTypes, f.profileErr
}

func (f *fakeQuerier) ProfileLabelValues(label, start, end string) ([]string, error) {
	f.capturedQuery = label
	f.capturedStart = start
	return f.profileValues, f.profileErr
}

func (f *fakeQuerier) SelectProfile(profileTypeID, selector, start, end string, top int) (*grafana.ProfileSummary, error) {
	f.capturedQuery = selector
	f.capturedType = profileTypeID
	f.capturedStart = start
	f.capturedLimit = top
	return f.profile, f.profileErr
}

var _ grafana.Querier = (*fakeQuerier)(nil)

func withFakeQuerier(t *testing.T, q *fakeQuerier) {
	t.Helper()
	orig := newQuerier
	newQuerier = func(_ string) (grafana.Querier, error) { return q, nil }
	t.Cleanup(func() { newQuerier = orig })
}

// -- query command --

func TestRunQuery_Success(t *testing.T) {
	q := &fakeQuerier{logsResult: []byte(`{"_msg":"hello"}`)}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"query", "-q", "severity:ERROR _time:1h"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedQuery != "severity:ERROR _time:1h" {
		t.Errorf("unexpected query: %q", q.capturedQuery)
	}
	if q.capturedLimit != 100 {
		t.Errorf("expected default limit 100, got %d", q.capturedLimit)
	}
}

func TestRunQuery_MissingQueryFlag(t *testing.T) {
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"query"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when -q flag is missing")
	}
}

func TestRunQuery_ClientError(t *testing.T) {
	q := &fakeQuerier{logsErr: errors.New("HTTP 401: Unauthorized")}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"query", "-q", "*"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error from client")
	}
}

func TestRunQuery_CustomLimit(t *testing.T) {
	q := &fakeQuerier{logsResult: []byte(`{"_msg":"hi"}`)}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"query", "-q", "*", "--limit", "5"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedLimit != 5 {
		t.Errorf("expected limit 5, got %d", q.capturedLimit)
	}
}

// -- metrics command --

func TestRunMetrics_DefaultFlags(t *testing.T) {
	q := &fakeQuerier{metricsResult: []byte(`{"status":"success"}`)}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"metrics", "-q", "up"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedStart != "now-1h" {
		t.Errorf("expected default start 'now-1h', got %q", q.capturedStart)
	}
	if q.capturedStep != "60s" {
		t.Errorf("expected default step '60s', got %q", q.capturedStep)
	}
}

func TestRunMetrics_CustomFlags(t *testing.T) {
	q := &fakeQuerier{metricsResult: []byte(`{"status":"success"}`)}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"metrics", "-q", "up", "--start", "now-6h", "--end", "now-1h", "--step", "5m"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedStart != "now-6h" {
		t.Errorf("unexpected start: %q", q.capturedStart)
	}
	if q.capturedStep != "5m" {
		t.Errorf("unexpected step: %q", q.capturedStep)
	}
}

func TestRunMetrics_MissingQueryFlag(t *testing.T) {
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"metrics"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when -q flag is missing")
	}
}

// -- instant command --

func TestRunInstant_EmptyTimePassedThrough(t *testing.T) {
	q := &fakeQuerier{instantResult: []byte(`{"status":"success"}`)}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"instant", "-q", "up"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedTime != "" {
		t.Errorf("expected empty time, got %q", q.capturedTime)
	}
}

func TestRunInstant_CustomTime(t *testing.T) {
	q := &fakeQuerier{instantResult: []byte(`{"status":"success"}`)}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"instant", "-q", "up", "--time", "2026-04-28T00:00:00Z"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedTime != "2026-04-28T00:00:00Z" {
		t.Errorf("unexpected time: %q", q.capturedTime)
	}
}

func TestRunInstant_MissingQueryFlag(t *testing.T) {
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"instant"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when -q flag is missing")
	}
}

// -- traces and profiles commands --

func TestRunTraces_DefaultsAndFlags(t *testing.T) {
	q := &fakeQuerier{hits: []grafana.TraceHit{{TraceID: "abc"}}}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"traces", "-q", "{ status = error }"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedQuery != "{ status = error }" || q.capturedStart != "now-1h" || q.capturedLimit != 20 {
		t.Errorf("query %q, start %q, limit %d", q.capturedQuery, q.capturedStart, q.capturedLimit)
	}

	cmd = buildRootCmd()
	cmd.SetArgs([]string{"traces", "-q", "{}", "--start", "now-5m", "--limit", "3"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedStart != "now-5m" || q.capturedLimit != 3 {
		t.Errorf("start %q, limit %d", q.capturedStart, q.capturedLimit)
	}
}

func TestRunTrace_PassesID(t *testing.T) {
	q := &fakeQuerier{trace: &grafana.TraceSummary{TraceID: "010203"}}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"trace", "010203"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedQuery != "010203" {
		t.Errorf("id = %q", q.capturedQuery)
	}
}

func TestRunProfile_DefaultsAndFlags(t *testing.T) {
	q := &fakeQuerier{profile: &grafana.ProfileSummary{Total: 1}}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"profile", "-s", `{service_name="x"}`})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedQuery != `{service_name="x"}` || q.capturedType != defaultProfileType || q.capturedLimit != 20 {
		t.Errorf("selector %q, type %q, limit %d", q.capturedQuery, q.capturedType, q.capturedLimit)
	}

	cmd = buildRootCmd()
	cmd.SetArgs([]string{"profile", "-s", "{}", "--type", "memory:alloc", "--limit", "5"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedType != "memory:alloc" || q.capturedLimit != 5 {
		t.Errorf("type %q, limit %d", q.capturedType, q.capturedLimit)
	}
}

func TestRunProfileLabels_DefaultsToServiceName(t *testing.T) {
	q := &fakeQuerier{profileValues: []string{"backend"}}
	withFakeQuerier(t, q)
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"profile-labels"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.capturedQuery != "service_name" {
		t.Errorf("label = %q", q.capturedQuery)
	}
}

func TestTraceAndProfileCommands_RequiredArgs(t *testing.T) {
	for _, args := range [][]string{{"traces"}, {"trace"}, {"profile"}} {
		cmd := buildRootCmd()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("%v: expected an error for missing arguments", args)
		}
	}
}

func TestProfileTypesCommand(t *testing.T) {
	withFakeQuerier(t, &fakeQuerier{profileTypes: []grafana.ProfileType{{ID: "cpu"}}})
	cmd := buildRootCmd()
	cmd.SetArgs([]string{"profile-types"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
