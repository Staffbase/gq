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
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Staffbase/gq/internal/grafana"
)

// Both formats are read by agents: they keep what an investigation needs and
// drop what costs tokens without telling anything (Grafana internals, values
// shown twice, links an agent cannot use).

// InstanceAlert is an alert with the name of the Grafana instance it came from
// ("" in single-instance mode).
type InstanceAlert struct {
	Instance string
	grafana.Alert
}

// AlertList is the list_alerts result. Labels every listed alert shares are in
// CommonLabels once instead of on each alert.
type AlertList struct {
	CommonLabels map[string]string `json:"commonLabels,omitempty"`
	Alerts       []AlertSummary    `json:"alerts"`
	// Errors maps the instances that could not be queried to the reason.
	Errors map[string]string `json:"errors,omitempty"`
	// More counts the alerts left out by the limit.
	More int `json:"more,omitempty"`
}

// AlertSummary is one alert of an AlertList.
type AlertSummary struct {
	Instance    string            `json:"instance,omitempty"`
	Fingerprint string            `json:"fingerprint"`
	State       string            `json:"state"`
	StartsAt    string            `json:"startsAt"`
	Summary     string            `json:"summary,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// CompactAlerts sorts the alerts (most severe, then newest first), keeps the
// first limit of them, and moves the labels they all share to CommonLabels.
func CompactAlerts(alerts []InstanceAlert, errs map[string]string, limit int) AlertList {
	alerts = slices.Clone(alerts)
	slices.SortStableFunc(alerts, func(a, b InstanceAlert) int {
		if c := severityRank(a.Labels["severity"]) - severityRank(b.Labels["severity"]); c != 0 {
			return c
		}
		if c := b.StartsAt.Compare(a.StartsAt); c != 0 {
			return c
		}
		return strings.Compare(a.Instance+"/"+a.Fingerprint, b.Instance+"/"+b.Fingerprint)
	})
	list := AlertList{Alerts: []AlertSummary{}}
	if len(errs) > 0 {
		list.Errors = errs
	}
	if len(alerts) > limit {
		list.More = len(alerts) - limit
		alerts = alerts[:limit]
	}
	if len(alerts) > 1 {
		list.CommonLabels = maps.Clone(alerts[0].Labels)
		for _, a := range alerts[1:] {
			maps.DeleteFunc(list.CommonLabels, func(k, v string) bool {
				av, ok := a.Labels[k]
				return !ok || av != v
			})
		}
		if len(list.CommonLabels) == 0 {
			list.CommonLabels = nil
		}
	}
	for _, a := range alerts {
		labels := maps.Clone(a.Labels)
		maps.DeleteFunc(labels, func(k, _ string) bool { _, common := list.CommonLabels[k]; return common })
		list.Alerts = append(list.Alerts, AlertSummary{
			Instance: a.Instance, Fingerprint: a.Fingerprint, State: a.Status.State,
			StartsAt: formatTime(a.StartsAt), Summary: oneLine(a.Annotations["summary"]), Labels: labels,
		})
	}
	return list
}

// severityRank orders severities, most severe first; unknown ones go last.
func severityRank(s string) int {
	switch strings.ToLower(s) {
	case "critical", "page":
		return 0
	case "high", "error":
		return 1
	case "warning", "warn":
		return 2
	case "info":
		return 3
	}
	return 4
}

// Labels and annotations shown elsewhere in FormatAlert, or Grafana internals
// that tell an investigation nothing.
var (
	headerLabels      = []string{"alertname", "severity", grafana.RuleUIDLabel, "grafana_folder"}
	hiddenAnnotations = []string{
		"summary", "description", "runbook_url", "__values__", "__value_string__", "__orgId__",
		"__alert_rule_namespace_uid__", "__dashboardUid__", "__panelId__",
	}
)

// FormatAlert renders an alert as compact Markdown for an agent: the header
// (what, where, since when, current values, labels) first, then description,
// rule with its query, and links. instance is "" in single-instance mode.
func FormatAlert(d *grafana.AlertDetail, instance string) string {
	a := d.Alert
	var b strings.Builder
	item := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "- %s: %s\n", k, v)
		}
	}

	fmt.Fprintf(&b, "# %s\n", oneLine(cmp.Or(a.Labels["alertname"], "(unnamed)")))
	if instance != "" {
		item("instance", oneLine(instance)+" ("+d.BaseURL+")")
	} else {
		item("grafana", d.BaseURL)
	}
	item("severity", strings.Join(nonEmpty(oneLine(a.Labels["severity"]),
		labeled("state", oneLine(a.Status.State)), labeled("since", formatTime(a.StartsAt))), " · "))
	item("fingerprint", oneLine(a.Fingerprint))
	item("summary", oneLine(a.Annotations["summary"]))
	item("runbook", oneLine(a.Annotations["runbook_url"]))
	item("values", formatValues(a.Annotations["__values__"]))
	var recv []string
	for _, r := range a.Receivers {
		recv = append(recv, r.Name)
	}
	item("receivers", oneLineJoin(recv))
	item("silenced by", oneLineJoin(a.Status.SilencedBy))
	item("inhibited by", oneLineJoin(a.Status.InhibitedBy))
	var labels []string
	for _, k := range slices.Sorted(maps.Keys(a.Labels)) {
		if !slices.Contains(headerLabels, k) {
			labels = append(labels, oneLine(k)+"="+strconv.Quote(a.Labels[k]))
		}
	}
	item("labels", strings.Join(labels, ", "))

	if v := strings.TrimSpace(a.Annotations["description"]); v != "" {
		b.WriteString("\n## Description\n\n" + strings.TrimPrefix(block(v), "\n"))
	}

	var other []string
	for _, k := range slices.Sorted(maps.Keys(a.Annotations)) {
		if !slices.Contains(hiddenAnnotations, k) {
			other = append(other, k)
		}
	}
	if len(other) > 0 {
		b.WriteString("\n## Other annotations\n\n")
		for _, k := range other {
			fmt.Fprintf(&b, "- %s: %s", oneLine(k), block(a.Annotations[k]))
		}
	}

	ruleLink := ""
	switch {
	case d.Rule != nil:
		r := d.Rule
		ruleLink = d.BaseURL + "/alerting/grafana/" + url.PathEscape(r.UID) + "/view"
		b.WriteString("\n## Rule\n\n")
		fmt.Fprintf(&b, "- %s\n", strings.Join(nonEmpty(oneLine(r.Name)+" (uid "+oneLine(r.UID)+")",
			labeled("folder", oneLine(r.Folder)), labeled("group", oneLine(r.Group)),
			labeled("health", oneLine(r.Health)), labeled("last eval", formatTime(r.LastEvaluation))), " · "))
		item("last error", oneLine(r.LastError))
		ds := oneLineJoin(r.DatasourceUIDs)
		if tool := queryTools[d.DatasourceKind]; tool != "" {
			ds += " (" + string(d.DatasourceKind) + ") → " + tool
		}
		item("datasource", ds)
		item("link", ruleLink)
		if q := stripComments(r.Query); q != "" {
			b.WriteString("\n" + fence(q))
		}
	case d.RuleErr != "":
		b.WriteString("\n## Rule\n\n")
		item("unavailable", oneLine(d.RuleErr))
	}

	b.WriteString("\n## Links\n\n")
	if uid := a.Annotations["__dashboardUid__"]; uid != "" {
		u := d.BaseURL + "/d/" + url.PathEscape(uid)
		if panel := a.Annotations["__panelId__"]; panel != "" {
			u += "?viewPanel=" + url.QueryEscape(panel)
		}
		item("dashboard", u)
	}
	if ruleLink == "" {
		item("generator", oneLine(a.GeneratorURL))
	}
	item("silence", silenceURL(d.BaseURL, a.Labels))
	return b.String()
}

// queryTools names the gq tool that queries each kind of datasource.
var queryTools = map[grafana.DatasourceKind]string{
	grafana.KindMetrics:  "query_metrics",
	grafana.KindLogs:     "query_logs",
	grafana.KindTraces:   "query_traces",
	grafana.KindProfiles: "query_profile",
}

// silenceURL opens Grafana's "new silence" form prefilled with all labels.
func silenceURL(base string, labels map[string]string) string {
	q := url.Values{"alertmanager": {"grafana"}}
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		q.Add("matcher", k+"="+labels[k])
	}
	return base + "/alerting/silence/new?" + q.Encode()
}

// formatValues renders Grafana's __values__ annotation ({"A":2.5}) as
// "A=2.5", keeping each number as Grafana wrote it.
func formatValues(raw string) string {
	var vals map[string]json.RawMessage
	if raw == "" || json.Unmarshal([]byte(raw), &vals) != nil {
		return ""
	}
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(vals)) {
		parts = append(parts, oneLine(k)+"="+oneLine(string(vals[k])))
	}
	return strings.Join(parts, ", ")
}

// stripComments drops the whole-line "#" comments of a PromQL or LogQL query.
// Trailing comments stay: a "#" inside a label value looks the same. Queries
// with backticks stay whole: a "#" line may be inside a multiline string.
func stripComments(q string) string {
	if strings.Contains(q, "`") {
		return strings.TrimSpace(q)
	}
	var keep []string
	for l := range strings.Lines(q) {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			keep = append(keep, l)
		}
	}
	return strings.TrimSpace(strings.Join(keep, ""))
}

// block renders a value on one line, or fenced when it spans several, so it
// cannot start Markdown of its own.
func block(v string) string {
	if strings.Contains(strings.TrimSpace(v), "\n") {
		return "\n" + fence(strings.TrimSpace(v))
	}
	return oneLine(v) + "\n"
}

// fence wraps s in a code fence longer than any backtick run inside s, so the
// value cannot close it.
func fence(s string) string {
	n, run := 3, 0
	for _, r := range s {
		if r == '`' {
			run++
			n = max(n, run+1)
		} else {
			run = 0
		}
	}
	f := strings.Repeat("`", n)
	return f + "\n" + s + "\n" + f + "\n"
}

// oneLine collapses whitespace, newlines included, so a value cannot start a
// new Markdown line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func oneLineJoin(ss []string) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = oneLine(s)
	}
	return strings.Join(out, ", ")
}

// formatTime is RFC 3339 in UTC, or "" for an unset time.
func formatTime(t time.Time) string {
	if t.IsZero() || t.Year() <= 1 {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// labeled returns "name: v", or "" when v is empty.
func labeled(name, v string) string {
	if v == "" {
		return ""
	}
	return name + ": " + v
}

func nonEmpty(ss ...string) []string {
	return slices.DeleteFunc(ss, func(s string) bool { return s == "" })
}
