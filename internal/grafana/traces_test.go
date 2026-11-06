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
	"strings"
	"testing"
)

const searchFixture = `{"traces":[{"traceID":"abc","rootServiceName":"web","rootTraceName":"GET /","startTimeUnixNano":"1700000000000000000","durationMs":5}]}`

// IDs are base64 as in Tempo's OTLP JSON: "AQID" = 010203, "BAUG" = 040506.
const traceFixture = `{"trace":{"resourceSpans":[
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},"scopeSpans":[{"spans":[
 {"traceId":"AQID","spanId":"AQID","parentSpanId":"","name":"GET /","startTimeUnixNano":"1000000000","endTimeUnixNano":"6000000000","status":{}}]}]},
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"db"}}]},"scopeSpans":[{"spans":[
 {"traceId":"AQID","spanId":"BAUG","parentSpanId":"AQID","name":"SELECT","startTimeUnixNano":"2000000000","endTimeUnixNano":"4000000000","status":{"code":"STATUS_CODE_ERROR"}}]}]}]}}`

// tracesServer serves body for any request and reports the last path and query.
func tracesServer(t *testing.T, body string) (c *Client, path, query *string) {
	t.Helper()
	path, query = new(string), new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*path, *query = r.URL.Path, r.URL.RawQuery
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}, path, query
}

func TestSearchTraces_ProxyPathParamsAndHits(t *testing.T) {
	c, path, query := tracesServer(t, searchFixture)
	c.TracesDatasourceUID = "tr"
	hits, err := c.SearchTraces(`{ status = error }`, "1700000000", "2023-11-14T22:13:20Z", 5)
	if err != nil {
		t.Fatal(err)
	}
	if *path != "/api/datasources/proxy/uid/tr/api/search" {
		t.Errorf("path = %s", *path)
	}
	for _, want := range []string{"q=%7B+status+%3D+error+%7D", "start=1700000000", "end=1700000000", "limit=5"} {
		if !strings.Contains(*query, want) {
			t.Errorf("query %q missing %q", *query, want)
		}
	}
	if len(hits) != 1 || hits[0].RootService != "web" || hits[0].RootOperation != "GET /" ||
		hits[0].DurationMs != 5 || hits[0].StartTime != "2023-11-14T22:13:20Z" {
		t.Errorf("unexpected hits: %+v", hits)
	}
}

func TestGetTrace_DefaultDatasourceUIDAndSummary(t *testing.T) {
	c, path, _ := tracesServer(t, traceFixture)
	s, err := c.GetTrace("010203")
	if err != nil {
		t.Fatal(err)
	}
	if *path != "/api/datasources/proxy/uid/victoriatraces/api/v2/traces/010203" {
		t.Errorf("path = %s", *path)
	}
	if s.TraceID != "010203" || s.RootService != "web" || s.RootOperation != "GET /" || s.DurationMs != 5000 ||
		s.ErrorSpans != 1 || len(s.ServiceMap) != 1 || s.ServiceMap[0] != (ServiceCall{From: "web", To: "db", Calls: 1}) {
		t.Errorf("unexpected summary: %+v", s)
	}
	if len(s.Spans) != 2 || s.Spans[1].SpanID != "040506" || s.Spans[1].ParentID != "010203" || !s.Spans[1].Error {
		t.Errorf("unexpected spans: %+v", s.Spans)
	}
}

func TestGetTrace_EmptyTrace(t *testing.T) {
	c, _, _ := tracesServer(t, `{"trace":{"resourceSpans":[]}}`)
	if _, err := c.GetTrace("abc"); err == nil {
		t.Fatal("expected error for a trace without spans")
	}
}

func TestSummarizeTrace_SingleServiceHasEmptyServiceMap(t *testing.T) {
	s, err := summarizeTrace([]byte(`{"trace":{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"traceId":"AQID","spanId":"AQID","startTimeUnixNano":"1","endTimeUnixNano":"2"}]}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.ServiceMap == nil || len(s.ServiceMap) != 0 {
		t.Errorf("ServiceMap should be an empty list, got %#v", s.ServiceMap)
	}
}

func TestIDToHex(t *testing.T) {
	const hex16, hex32 = "dd070f05c724999d", "f347f63cbb5ec6eddf4e28d5fc841b70"
	for in, want := range map[string]string{
		"AQID":         "010203",
		"3QcPBcckmZ0=": hex16,
		// Valid base64 as well as valid hex: must pass through, not be decoded.
		hex16: hex16,
		hex32: hex32,
	} {
		if got := idToHex(in); got != want {
			t.Errorf("idToHex(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestSummarizeTrace_BadTimestampsAreErrors(t *testing.T) {
	// A missing or malformed time must not become 0 and produce garbage offsets.
	for name, span := range map[string]string{
		"missing end": `{"traceId":"AQID","spanId":"AQID","startTimeUnixNano":"1"}`,
		"bad start":   `{"traceId":"AQID","spanId":"AQID","startTimeUnixNano":"x","endTimeUnixNano":"2"}`,
	} {
		raw := `{"trace":{"resourceSpans":[{"scopeSpans":[{"spans":[` + span + `]}]}]}}`
		if _, err := summarizeTrace([]byte(raw)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := summarizeSearch([]byte(`{"traces":[{"traceID":"a","startTimeUnixNano":"nope"}]}`)); err == nil {
		t.Error("search: expected an error for a bad start time")
	}
}

func TestGetTrace_RootPrefersParentlessOverEarlierOrphan(t *testing.T) {
	// The orphan DB span comes first in the payload and starts earlier.
	body := `{"trace":{"resourceSpans":[
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"db"}}]},"scopeSpans":[{"spans":[
 {"traceId":"AQID","spanId":"BAUG","parentSpanId":"BwgJ","name":"SELECT","startTimeUnixNano":"1000000000","endTimeUnixNano":"2000000000","status":{}}]}]},
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},"scopeSpans":[{"spans":[
 {"traceId":"AQID","spanId":"AQID","parentSpanId":"","name":"GET /","startTimeUnixNano":"2000000000","endTimeUnixNano":"6000000000","status":{}}]}]}]}}`
	c, _, _ := tracesServer(t, body)
	s, err := c.GetTrace("010203")
	if err != nil || s.RootService != "web" || s.RootOperation != "GET /" {
		t.Fatalf("root = %s %s, err = %v", s.RootService, s.RootOperation, err)
	}
}
