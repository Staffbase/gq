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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultTracesDatasourceUID is the UID mops provisions for VictoriaTraces on
// every observatory instance.
const DefaultTracesDatasourceUID = "victoriatraces"

// The datasource is of type tempo; its URL already ends in /select/tempo, so
// the proxy path continues with the plain Tempo API.

// SearchTraces runs a TraceQL search, e.g. `{ resource.service.name = "web" && status = error }`.
// Returns the raw Tempo JSON response.
func (c *Client) SearchTraces(query, start, end string, limit int) ([]byte, error) {
	params := url.Values{}
	params.Set("q", query)
	for k, v := range map[string]string{"start": start, "end": end} {
		sec, err := resolveSeconds(v)
		if err != nil {
			return nil, err
		}
		if sec != "" {
			params.Set(k, sec)
		}
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	return c.getTraces("api/search?" + params.Encode())
}

// GetTrace fetches one trace by ID. Returns the raw Tempo v2 JSON (OTLP) response.
func (c *Client) GetTrace(traceID string) ([]byte, error) {
	return c.getTraces("api/v2/traces/" + url.PathEscape(traceID))
}

func (c *Client) getTraces(path string) ([]byte, error) {
	uid := c.TracesDatasourceUID
	if uid == "" {
		uid = DefaultTracesDatasourceUID
	}
	endpoint := c.BaseURL + "/api/datasources/proxy/uid/" + uid + "/" + path
	resp, err := c.do("GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, readBody(resp.Body))
	}
	return io.ReadAll(resp.Body)
}

// resolveSeconds is resolveTime that also accepts RFC3339; Tempo wants Unix seconds.
func resolveSeconds(s string) (string, error) {
	r, err := resolveTime(s)
	if err != nil || r == "" {
		return "", err
	}
	if _, err := strconv.ParseInt(r, 10, 64); err == nil {
		return r, nil
	}
	t, err := time.Parse(time.RFC3339, r)
	if err != nil {
		return "", fmt.Errorf("invalid time %q", s)
	}
	return strconv.FormatInt(t.Unix(), 10), nil
}

// TraceHit is one search result: the headline of a trace.
type TraceHit struct {
	TraceID       string  `json:"traceId"`
	RootService   string  `json:"rootService"`
	RootOperation string  `json:"rootOperation"`
	StartTime     string  `json:"startTime"`
	DurationMs    float64 `json:"durationMs"`
}

// SummarizeSearch trims a Tempo search response to TraceHit values.
func SummarizeSearch(raw []byte) ([]TraceHit, error) {
	var r struct {
		Traces []struct {
			TraceID           string      `json:"traceID"`
			RootServiceName   string      `json:"rootServiceName"`
			RootTraceName     string      `json:"rootTraceName"`
			StartTimeUnixNano json.Number `json:"startTimeUnixNano"`
			DurationMs        float64     `json:"durationMs"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}
	out := make([]TraceHit, 0, len(r.Traces))
	for _, t := range r.Traces {
		out = append(out, TraceHit{
			TraceID: t.TraceID, RootService: t.RootServiceName, RootOperation: t.RootTraceName,
			StartTime: nanoToRFC3339(string(t.StartTimeUnixNano)), DurationMs: t.DurationMs,
		})
	}
	return out, nil
}

// TraceSummary is the LLM-friendly view of one trace.
type TraceSummary struct {
	TraceID       string        `json:"traceId"`
	RootService   string        `json:"rootService"`
	RootOperation string        `json:"rootOperation"`
	StartTime     string        `json:"startTime"`
	DurationMs    float64       `json:"durationMs"`
	SpanCount     int           `json:"spanCount"`
	ErrorSpans    int           `json:"errorSpans"`
	Services      []string      `json:"services"`
	ServiceMap    []ServiceCall `json:"serviceMap"`
	Spans         []SpanSummary `json:"spans"`
}

// ServiceCall is a parent→child service edge with its span count.
type ServiceCall struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Calls int    `json:"calls"`
}

// SpanSummary is a trimmed span.
type SpanSummary struct {
	SpanID     string  `json:"spanId"`
	ParentID   string  `json:"parentSpanId,omitempty"`
	Service    string  `json:"service"`
	Operation  string  `json:"operation"`
	StartMs    float64 `json:"startOffsetMs"`
	DurationMs float64 `json:"durationMs"`
	Error      bool    `json:"error,omitempty"`
}

type otlpValue struct {
	StringValue string `json:"stringValue"`
}

type otlpTrace struct {
	Trace struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []struct {
					Key   string    `json:"key"`
					Value otlpValue `json:"value"`
				} `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []struct {
					TraceID           string      `json:"traceId"`
					SpanID            string      `json:"spanId"`
					ParentSpanID      string      `json:"parentSpanId"`
					Name              string      `json:"name"`
					StartTimeUnixNano json.Number `json:"startTimeUnixNano"`
					EndTimeUnixNano   json.Number `json:"endTimeUnixNano"`
					Status            struct {
						Code any `json:"code"`
					} `json:"status"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	} `json:"trace"`
}

// SummarizeTrace turns a Tempo v2 (OTLP JSON) trace response into a TraceSummary.
func SummarizeTrace(raw []byte) (*TraceSummary, error) {
	var r otlpTrace
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decoding trace response: %w", err)
	}

	type span struct {
		SpanSummary
		start, end int64
	}
	var spans []span
	svcOf := map[string]string{}
	var traceID string
	for _, rs := range r.Trace.ResourceSpans {
		svc := ""
		for _, a := range rs.Resource.Attributes {
			if a.Key == "service.name" {
				svc = a.Value.StringValue
			}
		}
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				st, _ := strconv.ParseInt(string(s.StartTimeUnixNano), 10, 64)
				en, _ := strconv.ParseInt(string(s.EndTimeUnixNano), 10, 64)
				id := idToHex(s.SpanID)
				traceID = idToHex(s.TraceID)
				svcOf[id] = svc
				spans = append(spans, span{
					SpanSummary: SpanSummary{
						SpanID: id, ParentID: idToHex(s.ParentSpanID), Service: svc, Operation: s.Name,
						Error: isErrorStatus(s.Status.Code),
					},
					start: st, end: en,
				})
			}
		}
	}
	if len(spans) == 0 {
		return nil, fmt.Errorf("trace not found or has no spans")
	}

	sum := &TraceSummary{TraceID: traceID, SpanCount: len(spans), ServiceMap: []ServiceCall{}}
	minStart, maxEnd := spans[0].start, spans[0].end
	for _, s := range spans {
		minStart, maxEnd = min(minStart, s.start), max(maxEnd, s.end)
	}
	services := map[string]bool{}
	edges := map[[2]string]int{}
	for i := range spans {
		s := &spans[i]
		s.StartMs = float64(s.start-minStart) / 1e6
		s.DurationMs = float64(s.end-s.start) / 1e6
		services[s.Service] = true
		if s.Error {
			sum.ErrorSpans++
		}
		if ps, ok := svcOf[s.ParentID]; ok {
			if ps != s.Service {
				edges[[2]string{ps, s.Service}]++
			}
		} else if sum.RootOperation == "" {
			// First span whose parent is absent is the root.
			sum.RootService, sum.RootOperation = s.Service, s.Operation
		}
		sum.Spans = append(sum.Spans, s.SpanSummary)
	}
	sum.StartTime = time.Unix(0, minStart).UTC().Format(time.RFC3339Nano)
	sum.DurationMs = float64(maxEnd-minStart) / 1e6
	for s := range services {
		sum.Services = append(sum.Services, s)
	}
	sort.Strings(sum.Services)
	for e, n := range edges {
		sum.ServiceMap = append(sum.ServiceMap, ServiceCall{From: e[0], To: e[1], Calls: n})
	}
	sort.Slice(sum.ServiceMap, func(i, j int) bool {
		a, b := sum.ServiceMap[i], sum.ServiceMap[j]
		return a.From+"\x00"+a.To < b.From+"\x00"+b.To
	})
	sort.SliceStable(sum.Spans, func(i, j int) bool { return sum.Spans[i].StartMs < sum.Spans[j].StartMs })
	return sum, nil
}

// idToHex converts the base64 IDs OTLP JSON uses to hex; already-hex IDs pass through.
func idToHex(id string) string {
	b, err := base64.StdEncoding.DecodeString(id)
	if err != nil || len(b) == 0 {
		return id
	}
	return hex.EncodeToString(b)
}

// isErrorStatus accepts the enum as either its name or its number (2).
func isErrorStatus(code any) bool {
	switch v := code.(type) {
	case string:
		return strings.HasSuffix(v, "ERROR")
	case float64:
		return v == 2
	}
	return false
}

func nanoToRFC3339(ns string) string {
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(0, n).UTC().Format(time.RFC3339Nano)
}
