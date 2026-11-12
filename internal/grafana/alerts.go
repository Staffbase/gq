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
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"
)

// alertsPath is Grafana Alerting's Alertmanager API. It filters by label
// matchers and state itself, so gq never fetches all alerts to filter locally.
const alertsPath = "/api/alertmanager/grafana/api/v2/alerts"

// AlertFilter selects the alerts Grafana returns.
type AlertFilter struct {
	// Matchers are Alertmanager label matchers that must all match, e.g.
	// namespace="shop" or severity=~"critical|high".
	Matchers []string
	// State is "active" (the default when empty), "suppressed" (silenced or
	// inhibited), "unprocessed" or "all".
	State string
}

// Alert is one Grafana Alerting alert as the Alertmanager API returns it.
type Alert struct {
	Fingerprint string `json:"fingerprint"`
	Status      struct {
		State       string   `json:"state"`
		SilencedBy  []string `json:"silencedBy"`
		InhibitedBy []string `json:"inhibitedBy"`
	} `json:"status"`
	StartsAt     time.Time `json:"startsAt"`
	GeneratorURL string    `json:"generatorURL"`
	Receivers    []struct {
		Name string `json:"name"`
	} `json:"receivers"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

// ListAlerts returns the alerts matching f. Grafana does the filtering: a
// matcher it rejects comes back as an error.
func (c *Client) ListAlerts(f AlertFilter) ([]Alert, error) {
	q, err := alertQuery(f)
	if err != nil {
		return nil, err
	}
	var alerts []Alert
	if err := c.getJSON(alertsPath+"?"+q.Encode(), &alerts); err != nil {
		return nil, err
	}
	return alerts, nil
}

// rulesPath is Grafana's Prometheus-style rules API; it can select rules by UID.
const rulesPath = "/api/prometheus/grafana/api/v1/rules"

// ruleUIDLabel is the label Grafana puts the alert rule's UID in.
const ruleUIDLabel = "__alert_rule_uid__"

// AlertRule is the Grafana alert rule behind an alert.
type AlertRule struct {
	UID            string
	Name           string
	Group          string
	Folder         string
	Query          string
	DatasourceUIDs []string // the datasources the query reads
	Health         string
	LastError      string
	LastEvaluation time.Time
}

// AlertDetail is one alert with what an investigation needs next to it.
type AlertDetail struct {
	Alert Alert
	// Rule is nil when the alert has no rule UID or the rule could not be
	// loaded; RuleErr then says why.
	Rule    *AlertRule
	RuleErr string
	// BaseURL is the Grafana URL, for links.
	BaseURL string
	// QueryTool is the gq tool that runs the rule's query (e.g.
	// "query_metrics"), or "" when its datasource is not one gq knows.
	QueryTool string
}

// GetAlert returns the alert with the fingerprint and its rule. A non-empty
// alertname lets Grafana narrow the fetch; every state is searched.
func (c *Client) GetAlert(fingerprint, alertname string) (*AlertDetail, error) {
	f := AlertFilter{State: "all"}
	if alertname != "" {
		f.Matchers = []string{"alertname=" + strconv.Quote(alertname)}
	}
	alerts, err := c.ListAlerts(f)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(alerts, func(a Alert) bool { return a.Fingerprint == fingerprint })
	if i < 0 {
		return nil, fmt.Errorf("alert %s is not firing (resolved, or wrong instance, fingerprint or alertname)", fingerprint)
	}
	d := &AlertDetail{Alert: alerts[i], BaseURL: c.BaseURL}
	if uid := d.Alert.Labels[ruleUIDLabel]; uid != "" {
		// Best-effort: a missing rule must not hide the alert.
		if r, err := c.alertRule(uid); err != nil {
			d.RuleErr = err.Error()
		} else {
			d.Rule = r
			d.QueryTool = c.queryTool(r.DatasourceUIDs)
		}
	}
	return d, nil
}

// alertRule loads one rule by UID, without its alert instances.
func (c *Client) alertRule(uid string) (*AlertRule, error) {
	var resp struct {
		Data struct {
			Groups []struct {
				Name  string `json:"name"`
				File  string `json:"file"`
				Rules []struct {
					UID            string          `json:"uid"`
					Name           string          `json:"name"`
					Query          json.RawMessage `json:"query"`
					DatasourceUIDs []string        `json:"queriedDatasourceUIDs"`
					Health         string          `json:"health"`
					LastError      string          `json:"lastError"`
					LastEvaluation time.Time       `json:"lastEvaluation"`
				} `json:"rules"`
			} `json:"groups"`
		} `json:"data"`
	}
	q := url.Values{"rule_uid": {uid}, "limit_alerts": {"0"}}
	if err := c.getJSON(rulesPath+"?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	for _, g := range resp.Data.Groups {
		for _, r := range g.Rules {
			if r.UID == uid {
				return &AlertRule{
					UID: r.UID, Name: r.Name, Group: g.Name, Folder: g.File, Query: queryString(r.Query),
					DatasourceUIDs: r.DatasourceUIDs, Health: r.Health, LastError: r.LastError, LastEvaluation: r.LastEvaluation,
				}, nil
			}
		}
	}
	return nil, fmt.Errorf("rule %s not found", uid)
}

// queryString returns the rule query: Grafana sends a JSON string, but any JSON
// value is accepted.
func queryString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// queryTool names the gq tool for a rule reading exactly one datasource that
// this client is configured for.
func (c *Client) queryTool(uids []string) string {
	if len(uids) != 1 {
		return ""
	}
	switch uids[0] {
	case c.MetricsDatasourceUID:
		return "query_metrics"
	case c.LogsDatasourceUID:
		return "query_logs"
	case cmp.Or(c.TracesDatasourceUID, DefaultTracesDatasourceUID):
		return "query_traces"
	case cmp.Or(c.ProfilesDatasourceUID, DefaultProfilesDatasourceUID):
		return "query_profile"
	}
	return ""
}

// alertQuery turns f into the Alertmanager API's query: one filter param per
// matcher, and the state as its inclusion flags.
func alertQuery(f AlertFilter) (url.Values, error) {
	on := map[string]bool{}
	switch f.State {
	case "", "active":
		on["active"] = true
	case "suppressed":
		on["silenced"], on["inhibited"] = true, true
	case "unprocessed":
		on["unprocessed"] = true
	case "all":
		on["active"], on["silenced"], on["inhibited"], on["unprocessed"] = true, true, true, true
	default:
		return nil, fmt.Errorf("unknown state %q (want active, suppressed, unprocessed or all)", f.State)
	}
	q := url.Values{}
	for _, k := range []string{"active", "silenced", "inhibited", "unprocessed"} {
		q.Set(k, fmt.Sprint(on[k]))
	}
	if len(f.Matchers) > 0 {
		q["filter"] = f.Matchers
	}
	return q, nil
}

// getJSON GETs a Grafana API path (with auth and token refresh) and decodes the
// 200 response into dst.
func (c *Client) getJSON(path string, dst any) error {
	resp, err := c.do(http.MethodGet, c.BaseURL+path, nil, "")
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, readBody(io.LimitReader(resp.Body, 500)))
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
