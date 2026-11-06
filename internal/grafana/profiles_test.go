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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Nodes are [offset delta, total, self, name index]; root has total 100, self 10.
const profileFixture = `{"flamegraph":{"names":["total","main","work","main"],"total":"100",
"levels":[{"values":["0","100","10","0"]},{"values":["0","90","30","1","0","0","0","0"]},{"values":["0","60","60","2"]}]}}`

func profilesServer(t *testing.T, body string) (c *Client, path, reqBody *string) {
	t.Helper()
	path, reqBody = new(string), new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*path, *reqBody = r.URL.Path, string(b)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}, path, reqBody
}

func TestSelectProfile_ProxyPathBodyAndTopFunctions(t *testing.T) {
	c, path, body := profilesServer(t, profileFixture)
	got, err := c.SelectProfile("cpu", `{service_name="x"}`, "1700000000", "1700000060", 2)
	if err != nil {
		t.Fatal(err)
	}
	if *path != "/api/datasources/proxy/uid/pyroscope/querier.v1.QuerierService/SelectMergeStacktraces" {
		t.Errorf("path = %s", *path)
	}
	for _, want := range []string{`"profileTypeID":"cpu"`, `"start":1700000000000`, `"end":1700000060000`} {
		if !strings.Contains(*body, want) {
			t.Errorf("body %s missing %s", *body, want)
		}
	}
	// "main" appears on two nodes, so its self cost is summed: 30 + 0.
	if got.Total != 100 || len(got.TopFunctions) != 2 ||
		got.TopFunctions[0] != (ProfileFunc{Name: "work", Self: 60, SelfPct: 60}) ||
		got.TopFunctions[1] != (ProfileFunc{Name: "main", Self: 30, SelfPct: 30}) {
		t.Errorf("unexpected summary: %+v", got)
	}
}

func TestSelectProfile_EmptyProfile(t *testing.T) {
	c, _, _ := profilesServer(t, `{"flamegraph":{"names":[],"total":"0","levels":[]}}`)
	if _, err := c.SelectProfile("cpu", "{}", "", "", 5); err == nil {
		t.Fatal("expected error for an empty profile")
	}
}

func TestProfileTypesAndLabelValues(t *testing.T) {
	c, path, _ := profilesServer(t, `{"profileTypes":[{"ID":"cpu:1","name":"process_cpu","sampleType":"cpu","sampleUnit":"nanoseconds"}]}`)
	types, err := c.ProfileTypes("", "")
	if err != nil || len(types) != 1 || types[0] != (ProfileType{ID: "cpu:1", Name: "process_cpu", SampleType: "cpu", SampleUnit: "nanoseconds"}) {
		t.Fatalf("types = %+v, err = %v", types, err)
	}
	if !strings.HasSuffix(*path, "/ProfileTypes") {
		t.Errorf("path = %s", *path)
	}

	c, _, _ = profilesServer(t, `{"names":["backend","email-service"]}`)
	values, err := c.ProfileLabelValues("service_name", "", "")
	if err != nil || len(values) != 2 || values[0] != "backend" {
		t.Fatalf("values = %v, err = %v", values, err)
	}
}

func TestProfileRange_KeepsFractionalSeconds(t *testing.T) {
	c, _, body := profilesServer(t, `{"profileTypes":[]}`)
	if _, err := c.ProfileTypes("2023-11-14T22:13:20.100Z", "2023-11-14T22:13:20.900Z"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"start":1700000000100`, `"end":1700000000900`} {
		if !strings.Contains(*body, want) {
			t.Errorf("body %q missing %q", *body, want)
		}
	}
}
