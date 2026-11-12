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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Staffbase/gq/internal/grafana"
)

func detailFixture() *grafana.AlertDetail {
	var a grafana.Alert
	a.Fingerprint = "ccc"
	a.Status.State = "active"
	a.StartsAt = time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	a.GeneratorURL = "https://grafana.example.com/alerting/grafana/rule-1/view?orgId=1"
	a.Receivers = []grafana.Receiver{{Name: "team-shop"}}
	a.Labels = map[string]string{
		"alertname": "HighErrorRate", "severity": "critical", "__alert_rule_uid__": "rule-1",
		"grafana_folder": "Shop", "namespace": "shop", "service": "web",
	}
	a.Annotations = map[string]string{
		"summary":                      "Error rate above 1/s",
		"description":                  "Errors on web.\nCheck the logs.",
		"runbook_url":                  "https://runbooks.example.com/errors",
		"__values__":                   `{"A":2.5,"B":1}`,
		"__value_string__":             "[ var='A' value=2.5 ], [ var='B' value=1 ]",
		"__orgId__":                    "1",
		"__alert_rule_namespace_uid__": "shop-folder",
		"__dashboardUid__":             "dash1",
		"__panelId__":                  "4",
		"owner":                        "shop-team",
	}
	return &grafana.AlertDetail{
		Alert:   a,
		BaseURL: "https://grafana.example.com",
		Rule: &grafana.AlertRule{
			UID: "rule-1", Name: "HighErrorRate", Group: "shop-rules", Folder: "Shop", Health: "ok",
			Query: "# errors per second\nrate(errors_total[5m]) > 1", DatasourceUIDs: []string{"vm"},
			LastEvaluation: time.Date(2026, 1, 2, 10, 5, 0, 0, time.UTC),
		},
		DatasourceKind: grafana.KindMetrics,
	}
}

func TestFormatAlert_HeaderFirstWithoutNoise(t *testing.T) {
	want := "# HighErrorRate\n" +
		"- instance: prod (https://grafana.example.com)\n" +
		"- severity: critical · state: active · since: 2026-01-02T10:00:00Z\n" +
		"- fingerprint: ccc\n" +
		"- summary: Error rate above 1/s\n" +
		"- runbook: https://runbooks.example.com/errors\n" +
		"- values: A=2.5, B=1\n" +
		"- receivers: team-shop\n" +
		"- labels: namespace=\"shop\", service=\"web\"\n" +
		"\n## Description\n\n```\nErrors on web.\nCheck the logs.\n```\n" +
		"\n## Other annotations\n\n- owner: shop-team\n" +
		"\n## Rule\n\n" +
		"- HighErrorRate (uid rule-1) · folder: Shop · group: shop-rules · health: ok · last eval: 2026-01-02T10:05:00Z\n" +
		"- datasource: vm (metrics) → query_metrics\n" +
		"- link: https://grafana.example.com/alerting/grafana/rule-1/view\n" +
		"\n```\nrate(errors_total[5m]) > 1\n```\n" +
		"\n## Links\n\n" +
		"- dashboard: https://grafana.example.com/d/dash1?viewPanel=4\n" +
		"- silence: https://grafana.example.com/alerting/silence/new?alertmanager=grafana" +
		"&matcher=__alert_rule_uid__%3Drule-1&matcher=alertname%3DHighErrorRate&matcher=grafana_folder%3DShop" +
		"&matcher=namespace%3Dshop&matcher=service%3Dweb&matcher=severity%3Dcritical\n"
	if got := formatAlert(detailFixture(), "prod"); got != want {
		t.Errorf("formatAlert mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatAlert_OptionalParts(t *testing.T) {
	d := detailFixture()
	d.Alert.Status.State = "suppressed"
	d.Alert.Status.SilencedBy = []string{"s1", "s2"}
	d.Alert.Status.InhibitedBy = []string{"i1"}
	d.Alert.Annotations = map[string]string{"description": "one line"}
	d.Rule, d.RuleErr, d.DatasourceKind = nil, "HTTP 403: forbidden", ""

	got := formatAlert(d, "")
	for _, want := range []string{
		"- grafana: https://grafana.example.com\n", // single-instance mode: no instance name
		"- silenced by: s1, s2\n",
		"- inhibited by: i1\n",
		"\n## Description\n\none line\n",
		"\n## Rule\n\n- unavailable: HTTP 403: forbidden\n",
		"- generator: https://grafana.example.com/alerting/grafana/rule-1/view?orgId=1\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"## Other annotations", "- values:", "- runbook:", "- dashboard:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %q in:\n%s", unwanted, got)
		}
	}
}

func TestFormatAlert_ValuesCannotStartNewMarkdown(t *testing.T) {
	d := detailFixture()
	d.Alert.Annotations["summary"] = "fine\n## Rule\n- fake: injected"
	d.Alert.Annotations["description"] = "````\n## Links\n````"
	got := formatAlert(d, "prod")
	if !strings.Contains(got, "- summary: fine ## Rule - fake: injected\n") {
		t.Errorf("summary should collapse to one line:\n%s", got)
	}
	if !strings.Contains(got, "\n`````\n````\n## Links\n````\n`````\n") {
		t.Errorf("description fence should outrun the backticks inside:\n%s", got)
	}
}

func newInstanceAlert(instance, fp, severity, startsAt string, labels map[string]string) instanceAlert {
	var a grafana.Alert
	a.Fingerprint = fp
	a.Status.State = "active"
	a.StartsAt, _ = time.Parse(time.RFC3339, startsAt)
	a.Labels = map[string]string{"severity": severity}
	for k, v := range labels {
		a.Labels[k] = v
	}
	a.Annotations = map[string]string{"summary": "s-" + fp}
	return instanceAlert{Instance: instance, Alert: a}
}

func TestCompactAlerts_SharedLabelsOnceSortedBySeverityThenNewest(t *testing.T) {
	alerts := []instanceAlert{
		newInstanceAlert("dev", "a1", "warning", "2026-01-02T10:00:00Z", map[string]string{"alertname": "X", "job": "web"}),
		newInstanceAlert("prod", "a2", "critical", "2026-01-02T08:00:00Z", map[string]string{"alertname": "X", "job": "api"}),
		newInstanceAlert("prod", "a3", "warning", "2026-01-02T11:00:00Z", map[string]string{"alertname": "X", "job": "db"}),
	}
	out, err := json.Marshal(compactAlerts(alerts, map[string]string{"stage": "HTTP 403: forbidden"}, 100))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"commonLabels":{"alertname":"X"},"alerts":[` +
		`{"instance":"prod","fingerprint":"a2","state":"active","startsAt":"2026-01-02T08:00:00Z","summary":"s-a2","labels":{"job":"api","severity":"critical"}},` +
		`{"instance":"prod","fingerprint":"a3","state":"active","startsAt":"2026-01-02T11:00:00Z","summary":"s-a3","labels":{"job":"db","severity":"warning"}},` +
		`{"instance":"dev","fingerprint":"a1","state":"active","startsAt":"2026-01-02T10:00:00Z","summary":"s-a1","labels":{"job":"web","severity":"warning"}}],` +
		`"errors":{"stage":"HTTP 403: forbidden"}}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestCompactAlerts_SingleAlertKeepsItsLabels(t *testing.T) {
	alerts := []instanceAlert{newInstanceAlert("", "a1", "warning", "2026-01-02T10:00:00Z", map[string]string{"alertname": "X"})}
	out, _ := json.Marshal(compactAlerts(alerts, nil, 100))
	want := `{"alerts":[{"fingerprint":"a1","state":"active","startsAt":"2026-01-02T10:00:00Z","summary":"s-a1","labels":{"alertname":"X","severity":"warning"}}]}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestCompactAlerts_LimitCountsTheRest(t *testing.T) {
	alerts := []instanceAlert{
		newInstanceAlert("dev", "a1", "warning", "2026-01-02T10:00:00Z", nil),
		newInstanceAlert("dev", "a2", "critical", "2026-01-02T10:00:00Z", nil),
		newInstanceAlert("dev", "a3", "info", "2026-01-02T10:00:00Z", nil),
	}
	list := compactAlerts(alerts, nil, 2)
	if len(list.Alerts) != 2 || list.More != 1 || list.Alerts[0].Fingerprint != "a2" {
		t.Errorf("want the 2 most severe and more=1, got %+v", list)
	}
}

func TestCompactAlerts_EmptyIsAnEmptyList(t *testing.T) {
	out, _ := json.Marshal(compactAlerts(nil, nil, 100))
	if string(out) != `{"alerts":[]}` {
		t.Errorf("got %s", out)
	}
}
