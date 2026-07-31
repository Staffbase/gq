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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// tokenServer serves 401 to anything that is not wantToken, and 200 otherwise.
// It records every Authorization header it saw.
type tokenServer struct {
	*httptest.Server
	wantToken string

	mu   sync.Mutex
	seen []string
}

func newTokenServer(t *testing.T, wantToken string) *tokenServer {
	t.Helper()
	ts := &tokenServer{wantToken: wantToken}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		ts.mu.Lock()
		ts.seen = append(ts.seen, auth)
		ts.mu.Unlock()

		if auth != "Bearer "+ts.wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *tokenServer) headers() []string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]string(nil), ts.seen...)
}

func TestTokenCommand_RefreshesOn401AndRetries(t *testing.T) {
	srv := newTokenServer(t, "fresh-token")
	c := &Client{
		BaseURL:              srv.URL,
		Token:                "expired-token",
		TokenCommand:         "echo fresh-token",
		LogsDatasourceUID:    "victorialogs",
		MetricsDatasourceUID: "victoriametrics",
	}

	if _, err := c.QueryMetricsInstant("up", ""); err != nil {
		t.Fatalf("expected the retry to succeed, got: %v", err)
	}

	seen := srv.headers()
	if len(seen) != 2 {
		t.Fatalf("expected exactly 2 requests (original + retry), got %d: %v", len(seen), seen)
	}
	if seen[0] != "Bearer expired-token" {
		t.Errorf("first request should carry the stale token, got %q", seen[0])
	}
	if seen[1] != "Bearer fresh-token" {
		t.Errorf("retry should carry the refreshed token, got %q", seen[1])
	}
	if c.Token != "fresh-token" {
		t.Errorf("refreshed token should be stored on the client, got %q", c.Token)
	}
}

func TestTokenCommand_RetriesOnlyOnce(t *testing.T) {
	// The command yields a token the server still rejects, so the retry 401s too.
	// That second 401 must surface as an error rather than loop.
	srv := newTokenServer(t, "the-only-good-token")
	c := &Client{
		BaseURL:              srv.URL,
		Token:                "stale",
		TokenCommand:         "echo still-wrong",
		LogsDatasourceUID:    "victorialogs",
		MetricsDatasourceUID: "victoriametrics",
	}

	_, err := c.QueryMetricsInstant("up", "")
	if err == nil {
		t.Fatal("expected an error when the refreshed token is also rejected")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
	if n := len(srv.headers()); n != 2 {
		t.Errorf("expected exactly 2 requests, got %d", n)
	}
}

func TestTokenCommand_NotRunWithoutTokenCommand(t *testing.T) {
	srv := newTokenServer(t, "good")
	c := &Client{
		BaseURL:              srv.URL,
		Token:                "bad",
		LogsDatasourceUID:    "victorialogs",
		MetricsDatasourceUID: "victoriametrics",
	}

	if _, err := c.QueryMetricsInstant("up", ""); err == nil {
		t.Fatal("expected a 401 error when no token_command is configured")
	}
	if n := len(srv.headers()); n != 1 {
		t.Errorf("expected no retry without token_command, got %d requests", n)
	}
}

func TestTokenCommand_NotRunForCookieAuth(t *testing.T) {
	// Cookie beats Token when both are set, so a fresh token could not change
	// the outcome. Running the command here would be pure noise.
	srv := newTokenServer(t, "unreachable")
	c := &Client{
		BaseURL:              srv.URL,
		Cookie:               "grafana_session=rejected",
		Token:                "unused",
		TokenCommand:         "echo should-not-run",
		LogsDatasourceUID:    "victorialogs",
		MetricsDatasourceUID: "victoriametrics",
	}

	if _, err := c.QueryMetricsInstant("up", ""); err == nil {
		t.Fatal("expected a 401 error for a rejected cookie")
	}
	if n := len(srv.headers()); n != 1 {
		t.Errorf("expected no retry for cookie auth, got %d requests", n)
	}
	if c.Token != "unused" {
		t.Errorf("token should be untouched for cookie auth, got %q", c.Token)
	}
}

func TestTokenCommand_FailureIsReported(t *testing.T) {
	srv := newTokenServer(t, "good")
	c := &Client{
		BaseURL:              srv.URL,
		Token:                "bad",
		TokenCommand:         "echo 'no credentials' >&2; exit 3",
		LogsDatasourceUID:    "victorialogs",
		MetricsDatasourceUID: "victoriametrics",
	}

	_, err := c.QueryMetricsInstant("up", "")
	if err == nil {
		t.Fatal("expected an error when token_command fails")
	}
	// The exit code and stderr are the only clue the user gets about why
	// authentication is not recovering — keep them in the message.
	for _, want := range []string{"token refresh failed", "exited 3", "no credentials"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in error, got: %v", want, err)
		}
	}
}

func TestTokenCommand_EmptyOutputIsAnError(t *testing.T) {
	srv := newTokenServer(t, "good")
	c := &Client{
		BaseURL:              srv.URL,
		Token:                "bad",
		TokenCommand:         "true",
		LogsDatasourceUID:    "victorialogs",
		MetricsDatasourceUID: "victoriametrics",
	}

	_, err := c.QueryMetricsInstant("up", "")
	if err == nil || !strings.Contains(err.Error(), "empty output") {
		t.Fatalf("expected an empty-output error, got: %v", err)
	}
}

// A burst of concurrent 401s must refresh once, not once per request. Run under
// -race, this is also the regression test for the unsynchronised Token field.
func TestTokenCommand_ConcurrentRefreshRunsCommandOnce(t *testing.T) {
	srv := newTokenServer(t, "fresh-token")

	// The command appends a byte per run, so the count survives the subshell.
	counter := filepath.Join(t.TempDir(), "runs")
	c := &Client{
		BaseURL:              srv.URL,
		Token:                "expired-token",
		TokenCommand:         fmt.Sprintf("printf x >> %s; echo fresh-token", counter),
		LogsDatasourceUID:    "victorialogs",
		MetricsDatasourceUID: "victoriametrics",
	}

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			_, errs[i] = c.QueryMetricsInstant(fmt.Sprintf(`up{i="%d"}`, i), "")
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d failed: %v", i, err)
		}
	}

	// Exactly one run: whichever goroutine gets there first replaces the token,
	// and the rest either see the new value before sending or are told about it
	// by refreshToken. Without the lock this is 2..8.
	runs, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("reading the counter file: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected token_command to run once, ran %d times", len(runs))
	}

	// Goroutines that started after the refresh never send a stale token, so the
	// request count is not fixed — but no request may carry an unknown token.
	for _, h := range srv.headers() {
		if h != "Bearer expired-token" && h != "Bearer fresh-token" {
			t.Errorf("unexpected Authorization header %q", h)
		}
	}
	if c.Token != "fresh-token" {
		t.Errorf("expected the refreshed token to stick, got %q", c.Token)
	}
}
