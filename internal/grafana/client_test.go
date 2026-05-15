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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// -- NewClientFromEnv tests --

func TestNewClientFromEnv_MissingURL(t *testing.T) {
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_COOKIE", "session=abc")
	_, err := NewClientFromEnv()
	if err == nil {
		t.Fatal("expected error when GRAFANA_URL is unset")
	}
}

func TestNewClientFromEnv_MissingAuth(t *testing.T) {
	t.Setenv("GRAFANA_URL", "http://grafana.example.com")
	t.Setenv("GRAFANA_COOKIE", "")
	t.Setenv("GRAFANA_TOKEN", "")
	_, err := NewClientFromEnv()
	if err == nil {
		t.Fatal("expected error when neither GRAFANA_COOKIE nor GRAFANA_TOKEN is set")
	}
}

func TestNewClientFromEnv_CookieAuth(t *testing.T) {
	t.Setenv("GRAFANA_URL", "http://grafana.example.com")
	t.Setenv("GRAFANA_COOKIE", "grafana_session=abc123")
	t.Setenv("GRAFANA_TOKEN", "")
	c, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Cookie != "grafana_session=abc123" {
		t.Errorf("expected cookie set, got %q", c.Cookie)
	}
}

func TestNewClientFromEnv_TokenAuth(t *testing.T) {
	t.Setenv("GRAFANA_URL", "http://grafana.example.com")
	t.Setenv("GRAFANA_COOKIE", "")
	t.Setenv("GRAFANA_TOKEN", "glsa_token123")
	c, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Token != "glsa_token123" {
		t.Errorf("expected token set, got %q", c.Token)
	}
}

func TestNewClientFromEnv_CookiePrecedence(t *testing.T) {
	t.Setenv("GRAFANA_URL", "http://grafana.example.com")
	t.Setenv("GRAFANA_COOKIE", "grafana_session=abc")
	t.Setenv("GRAFANA_TOKEN", "glsa_token")
	c, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both set — cookie takes precedence in do()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "" {
			t.Error("expected Cookie header, got none")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no Authorization header when cookie is set")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c.BaseURL = srv.URL
	_, _ = c.do("GET", srv.URL, nil, "")
}

// -- helper: newTestClient --

func newTestClient(srv *httptest.Server) *Client {
	return &Client{
		BaseURL:              srv.URL,
		Cookie:               "session=test",
		LogsDatasourceUID:    "victorialogs",
		MetricsDatasourceUID: "victoriametrics",
	}
}

// -- QueryLogs tests --

func TestQueryLogs_Success(t *testing.T) {
	lines := `{"_msg":"hello","severity":"INFO"}` + "\n" + `{"_msg":"world","severity":"ERROR"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/select/logsql/query") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parsing form: %v", err)
		}
		if r.FormValue("query") != `severity:ERROR` {
			t.Errorf("unexpected query: %q", r.FormValue("query"))
		}
		if r.FormValue("limit") != "5" {
			t.Errorf("unexpected limit: %q", r.FormValue("limit"))
		}
		if _, err := fmt.Fprint(w, lines); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.BaseURL = srv.URL
	out, err := c.QueryLogs("severity:ERROR", "", "", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != lines {
		t.Errorf("unexpected output: %q", string(out))
	}
}

func TestQueryLogs_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.QueryLogs("*", "", "", 10)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}

func TestQueryLogs_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	out, err := c.QueryLogs("*", "", "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty output, got %q", string(out))
	}
}

func TestQueryLogs_SendsCookieHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "session=test" {
			t.Errorf("expected cookie header, got %q", r.Header.Get("Cookie"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, _ = c.QueryLogs("*", "", "", 10)
}

func TestQueryLogs_SendsBearerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer glsa_abc" {
			t.Errorf("expected Bearer header, got %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "glsa_abc", LogsDatasourceUID: "victorialogs", MetricsDatasourceUID: "victoriametrics"}
	_, _ = c.QueryLogs("*", "", "", 10)
}

// -- QueryMetricsRange tests --

func TestQueryMetricsRange_Success(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/api/v1/query_range") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") != `up{namespace="flink"}` {
			t.Errorf("unexpected query param: %q", r.URL.Query().Get("query"))
		}
		if r.URL.Query().Get("step") != "60s" {
			t.Errorf("unexpected step: %q", r.URL.Query().Get("step"))
		}
		if _, err := fmt.Fprint(w, payload); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv)
	out, err := c.QueryMetricsRange(`up{namespace="flink"}`, "now-1h", "now", "60s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != payload {
		t.Errorf("unexpected output: %q", string(out))
	}
}

func TestQueryMetricsRange_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.QueryMetricsRange("bad{", "now-1h", "now", "60s")
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 in error, got: %v", err)
	}
}

// -- QueryMetricsInstant tests --

func TestQueryMetricsInstant_Success(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"vector","result":[]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/v1/query") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// time param should be absent when empty
		if r.URL.Query().Get("time") != "" {
			t.Errorf("expected no time param, got %q", r.URL.Query().Get("time"))
		}
		if _, err := fmt.Fprint(w, payload); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv)
	out, err := c.QueryMetricsInstant("up", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != payload {
		t.Errorf("unexpected output: %q", string(out))
	}
}

func TestQueryMetricsInstant_WithTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("time") != "2026-04-28T00:00:00Z" {
			t.Errorf("expected time param, got %q", r.URL.Query().Get("time"))
		}
		if _, err := fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.QueryMetricsInstant("up", "2026-04-28T00:00:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// -- ListLabelValues tests --

func TestListLabelValues_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/v1/label/namespace/values") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   []string{"flink", "monitoring", "kube-system"},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	values, err := c.ListLabelValues("namespace", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 3 {
		t.Errorf("expected 3 values, got %d", len(values))
	}
}

func TestListLabelValues_WithMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("match[]") != `{namespace="flink"}` {
			t.Errorf("expected match[] param, got %q", r.URL.Query().Get("match[]"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   []string{"flink"},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.ListLabelValues("namespace", `{namespace="flink"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListLabelValues_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.ListLabelValues("namespace", "")
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 in error, got: %v", err)
	}
}

// Ensure *Client satisfies the Querier interface at compile time.
var _ Querier = (*Client)(nil)

// Unset env vars that could leak between tests.
func init() {
	_ = os.Unsetenv("GRAFANA_URL")
	_ = os.Unsetenv("GRAFANA_COOKIE")
	_ = os.Unsetenv("GRAFANA_TOKEN")
}
