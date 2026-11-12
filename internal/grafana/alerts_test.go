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
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

const alertsFixture = `[
 {"fingerprint":"aaa","status":{"state":"active"},"startsAt":"2026-01-02T10:00:00Z",
  "labels":{"alertname":"HighErrorRate","severity":"warning","namespace":"shop"}},
 {"fingerprint":"bbb","status":{"state":"active"},"startsAt":"2026-01-02T09:00:00Z",
  "labels":{"alertname":"PodCrashLooping","severity":"critical","namespace":"shop"}}
]`

// alertsServer answers each path from bodies (404 otherwise) and records every
// request's path and query.
func alertsServer(t *testing.T, bodies map[string]string) (*Client, *[]*url.URL) {
	t.Helper()
	var reqs []*url.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r.URL)
		body, ok := bodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}, &reqs
}

func TestListAlerts_RejectedMatcherIsAnError(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		http.Error(w, `{"message":"bad matcher"}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	c := &Client{BaseURL: srv.URL}

	_, err := c.ListAlerts(AlertFilter{Matchers: []string{`namespace=shop"`}})
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "bad matcher") {
		t.Fatalf("want Grafana's 400 as the error, got %v", err)
	}
	if n != 1 {
		t.Errorf("want 1 request (no unfiltered retry), got %d", n)
	}
}

func TestListAlerts_UnknownStateIsAnError(t *testing.T) {
	c, reqs := alertsServer(t, map[string]string{alertsPath: alertsFixture})
	if _, err := c.ListAlerts(AlertFilter{State: "firing"}); err == nil || !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("want an unknown-state error, got %v", err)
	}
	if len(*reqs) != 0 {
		t.Errorf("want no request for a bad state, got %d", len(*reqs))
	}
}

func TestListAlerts_DecodesAlerts(t *testing.T) {
	c, _ := alertsServer(t, map[string]string{alertsPath: alertsFixture})
	alerts, err := c.ListAlerts(AlertFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 || alerts[0].Fingerprint != "aaa" || alerts[0].Labels["namespace"] != "shop" ||
		alerts[0].Status.State != "active" || alerts[0].StartsAt.IsZero() {
		t.Errorf("unexpected alerts: %+v", alerts)
	}
}

func TestListAlerts_GrafanaFiltersByMatchersAndState(t *testing.T) {
	tests := []struct {
		state string
		want  url.Values
	}{
		{"", url.Values{"active": {"true"}, "silenced": {"false"}, "inhibited": {"false"}, "unprocessed": {"false"}}},
		{"active", url.Values{"active": {"true"}, "silenced": {"false"}, "inhibited": {"false"}, "unprocessed": {"false"}}},
		{"suppressed", url.Values{"active": {"false"}, "silenced": {"true"}, "inhibited": {"true"}, "unprocessed": {"false"}}},
		{"unprocessed", url.Values{"active": {"false"}, "silenced": {"false"}, "inhibited": {"false"}, "unprocessed": {"true"}}},
		{"all", url.Values{"active": {"true"}, "silenced": {"true"}, "inhibited": {"true"}, "unprocessed": {"true"}}},
	}
	for _, tt := range tests {
		t.Run("state="+tt.state, func(t *testing.T) {
			c, reqs := alertsServer(t, map[string]string{alertsPath: alertsFixture})
			matchers := []string{`namespace="shop"`, `severity=~"critical|warning"`}
			if _, err := c.ListAlerts(AlertFilter{Matchers: matchers, State: tt.state}); err != nil {
				t.Fatal(err)
			}
			if len(*reqs) != 1 {
				t.Fatalf("want 1 request, got %d", len(*reqs))
			}
			got := (*reqs)[0].Query()
			if !slices.Equal(got["filter"], matchers) {
				t.Errorf("filter = %q, want %q", got["filter"], matchers)
			}
			for k, v := range tt.want {
				if !slices.Equal(got[k], v) {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

const ruleAlertFixture = `[{"fingerprint":"ccc","status":{"state":"active"},"startsAt":"2026-01-02T10:00:00Z",
 "labels":{"alertname":"HighErrorRate","__alert_rule_uid__":"rule-1","namespace":"shop"}}]`

const rulesFixture = `{"data":{"groups":[{"name":"shop-rules","file":"Shop","rules":[
 {"uid":"rule-1","name":"HighErrorRate","query":"# errors per second\nrate(errors_total[5m]) > 1",
  "queriedDatasourceUIDs":["vm"],"health":"ok","lastError":"","lastEvaluation":"2026-01-02T10:05:00Z"}]}]}}`

func TestGetAlert_NarrowsByAlertnameAndLoadsItsRule(t *testing.T) {
	c, reqs := alertsServer(t, map[string]string{alertsPath: ruleAlertFixture, rulesPath: rulesFixture})
	c.MetricsDatasourceUID = "vm"

	d, err := c.GetAlert("ccc", "HighErrorRate")
	if err != nil {
		t.Fatal(err)
	}
	if len(*reqs) != 2 {
		t.Fatalf("want an alerts and a rules request, got %d", len(*reqs))
	}
	aq := (*reqs)[0].Query()
	if !slices.Equal(aq["filter"], []string{`alertname="HighErrorRate"`}) || aq.Get("silenced") != "true" {
		t.Errorf("alerts query should narrow by alertname over all states, got %v", aq)
	}
	rq := (*reqs)[1].Query()
	if rq.Get("rule_uid") != "rule-1" || rq.Get("limit_alerts") != "0" {
		t.Errorf("rules query = %v", rq)
	}
	if d.Alert.Fingerprint != "ccc" || d.Rule == nil || d.Rule.Name != "HighErrorRate" ||
		d.Rule.Group != "shop-rules" || d.Rule.Folder != "Shop" || !strings.Contains(d.Rule.Query, "rate(errors_total") {
		t.Errorf("unexpected detail: %+v rule %+v", d.Alert, d.Rule)
	}
	if d.QueryTool != "query_metrics" {
		t.Errorf("QueryTool = %q, want query_metrics", d.QueryTool)
	}
	if d.BaseURL != c.BaseURL {
		t.Errorf("BaseURL = %q", d.BaseURL)
	}
}

func TestGetAlert_DatasourceMapsToItsTool(t *testing.T) {
	for uids, want := range map[string]string{
		`"vm"`: "query_metrics", `"vl"`: "query_logs",
		`"victoriatraces"`: "query_traces", `"pyroscope"`: "query_profile",
		`"other"`: "", `"vm","vl"`: "", // several datasources: no single tool
		`"vm","vm"`: "query_metrics", // Grafana lists one UID per query
	} {
		rules := strings.Replace(rulesFixture, `["vm"]`, "["+uids+"]", 1)
		c, _ := alertsServer(t, map[string]string{alertsPath: ruleAlertFixture, rulesPath: rules})
		c.LogsDatasourceUID, c.MetricsDatasourceUID = "vl", "vm"
		d, err := c.GetAlert("ccc", "")
		if err != nil {
			t.Fatal(err)
		}
		if d.QueryTool != want {
			t.Errorf("datasources %s: QueryTool = %q, want %q", uids, d.QueryTool, want)
		}
	}
}

func TestGetAlert_RuleFailureKeepsTheAlert(t *testing.T) {
	c, _ := alertsServer(t, map[string]string{alertsPath: ruleAlertFixture})
	d, err := c.GetAlert("ccc", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Rule != nil || !strings.Contains(d.RuleErr, "404") {
		t.Errorf("want no rule and a 404 RuleErr, got rule %+v err %q", d.Rule, d.RuleErr)
	}
}

func TestGetAlert_UnknownFingerprint(t *testing.T) {
	c, _ := alertsServer(t, map[string]string{alertsPath: ruleAlertFixture})
	if _, err := c.GetAlert("zzz", ""); err == nil || !strings.Contains(err.Error(), "not firing") {
		t.Fatalf("want a not-firing error, got %v", err)
	}
}
