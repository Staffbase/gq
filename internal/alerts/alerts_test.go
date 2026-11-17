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

package alerts

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Staffbase/gq/internal/grafana"
)

// fakeGrafana answers the alert calls of one instance. The other Querier
// methods are not used here.
type fakeGrafana struct {
	grafana.Querier
	alerts    []grafana.Alert
	alertsErr error
	detail    *grafana.AlertDetail

	filter      grafana.AlertFilter
	listCalls   int
	fingerprint string
	alertname   string
}

func (f *fakeGrafana) ListAlerts(filter grafana.AlertFilter) ([]grafana.Alert, error) {
	f.filter = filter
	f.listCalls++
	return f.alerts, f.alertsErr
}

func (f *fakeGrafana) GetAlert(fingerprint, alertname string) (*grafana.AlertDetail, error) {
	f.fingerprint, f.alertname = fingerprint, alertname
	if f.detail == nil {
		return nil, errors.New("alert not found")
	}
	return f.detail, nil
}

// instances serves one fake per name; unknown names fail like an unknown
// registry instance.
func instances(fakes map[string]*fakeGrafana, names ...string) grafana.Instances {
	return grafana.Instances{Names: names, Get: func(name string) (grafana.Querier, error) {
		if f, ok := fakes[name]; ok {
			return f, nil
		}
		return nil, fmt.Errorf("instance %q not found", name)
	}}
}

func alert(fp, alertname, namespace string) grafana.Alert {
	var a grafana.Alert
	a.Fingerprint = fp
	a.Status.State = "active"
	a.StartsAt = time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	a.Labels = map[string]string{"alertname": alertname, "namespace": namespace, "severity": "warning"}
	return a
}

// -- List --

func TestList_AllInstancesAtOnce(t *testing.T) {
	fakes := map[string]*fakeGrafana{
		"dev":   {alerts: []grafana.Alert{alert("d1", "HighErrorRate", "shop")}},
		"prod":  {alerts: []grafana.Alert{alert("p1", "HighErrorRate", "cart")}},
		"stage": {alertsErr: errors.New("HTTP 403: forbidden")},
	}
	f := grafana.AlertFilter{Matchers: []string{`alertname="HighErrorRate"`}, State: grafana.StateSuppressed}

	list, err := List(instances(fakes, "dev", "prod", "stage"), "", f, DefaultLimit)
	if err != nil {
		t.Fatalf("one failing instance must not fail the call: %v", err)
	}
	if len(list.Alerts) != 2 || list.CommonLabels["alertname"] != "HighErrorRate" {
		t.Errorf("want both instances' alerts with the shared alertname hoisted, got %+v", list)
	}
	if list.Errors["stage"] != "HTTP 403: forbidden" {
		t.Errorf("want stage's error, got %v", list.Errors)
	}
	for name, q := range fakes {
		if !slices.Equal(q.filter.Matchers, f.Matchers) || q.filter.State != grafana.StateSuppressed {
			t.Errorf("%s: filter not passed to Grafana: %+v", name, q.filter)
		}
	}
}

func TestList_OneInstance(t *testing.T) {
	fakes := map[string]*fakeGrafana{
		"dev":  {alerts: []grafana.Alert{alert("d1", "X", "shop")}},
		"prod": {alertsErr: errors.New("must not be queried")},
	}
	list, err := List(instances(fakes, "dev", "prod"), "dev", grafana.AlertFilter{}, DefaultLimit)
	if err != nil || len(list.Alerts) != 1 || list.Alerts[0].Fingerprint != "d1" {
		t.Fatalf("want dev's alert, got %+v, %v", list, err)
	}
	if fakes["prod"].listCalls != 0 {
		t.Error("prod must not be queried when instance is dev")
	}
}

func TestList_OneInstanceErrorIsTheError(t *testing.T) {
	fakes := map[string]*fakeGrafana{"dev": {alertsErr: errors.New("HTTP 400: bad matcher")}}
	in := instances(fakes, "dev", "prod")
	if _, err := List(in, "dev", grafana.AlertFilter{Matchers: []string{`x=`}}, DefaultLimit); err == nil || !strings.Contains(err.Error(), "bad matcher") {
		t.Errorf("want Grafana's error, got %v", err)
	}
	if _, err := List(in, "nope", grafana.AlertFilter{}, DefaultLimit); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("want an unknown-instance error, got %v", err)
	}
}

func TestList_BadStateFailsBeforeAskingAnyone(t *testing.T) {
	fakes := map[string]*fakeGrafana{"dev": {}, "prod": {}}
	_, err := List(instances(fakes, "dev", "prod"), "", grafana.AlertFilter{State: "firing"}, DefaultLimit)
	if err == nil || !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("want an unknown-state error, got %v", err)
	}
	if fakes["dev"].listCalls+fakes["prod"].listCalls != 0 {
		t.Error("no instance should be asked with a bad state")
	}
}

func TestList_EveryInstanceFailingIsAnError(t *testing.T) {
	bad := errors.New("HTTP 400: bad matcher")
	fakes := map[string]*fakeGrafana{
		"dev": {alertsErr: bad}, "prod": {alertsErr: bad}, "stage": {alertsErr: errors.New("HTTP 403: forbidden")},
	}
	_, err := List(instances(fakes, "dev", "prod", "stage"), "", grafana.AlertFilter{Matchers: []string{`x=~"("`}}, DefaultLimit)
	if err == nil {
		t.Fatal("want an error when no instance answered")
	}
	want := "no instance answered: dev, prod: HTTP 400: bad matcher; stage: HTTP 403: forbidden"
	if err.Error() != want {
		t.Errorf("want each distinct error once with its instances\ngot  %q\nwant %q", err, want)
	}
}

func TestList_LimitCountsTheRest(t *testing.T) {
	fakes := map[string]*fakeGrafana{"": {alerts: []grafana.Alert{alert("a", "X", "s"), alert("b", "X", "s"), alert("c", "X", "s")}}}
	list, err := List(instances(fakes, ""), "", grafana.AlertFilter{}, 2)
	if err != nil || len(list.Alerts) != 2 || list.More != 1 {
		t.Errorf("want 2 alerts and more=1, got %+v, %v", list, err)
	}
}

func TestList_SingleInstanceHasNoInstanceNames(t *testing.T) {
	fakes := map[string]*fakeGrafana{"": {alerts: []grafana.Alert{alert("a", "X", "s")}}}
	list, err := List(instances(fakes, ""), "", grafana.AlertFilter{}, DefaultLimit)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(list)
	if strings.Contains(string(out), `"instance"`) {
		t.Errorf("want alerts without instance names, got %s", out)
	}
}

// -- Describe --

func TestDescribe_Markdown(t *testing.T) {
	prod := &fakeGrafana{detail: &grafana.AlertDetail{Alert: alert("p1", "HighErrorRate", "shop"), BaseURL: "https://grafana.example.com"}}
	in := instances(map[string]*fakeGrafana{"prod": prod}, "dev", "prod")

	text, err := Describe(in, "prod", "p1", "HighErrorRate")
	if err != nil {
		t.Fatal(err)
	}
	if prod.fingerprint != "p1" || prod.alertname != "HighErrorRate" {
		t.Errorf("fingerprint/alertname not passed: %q %q", prod.fingerprint, prod.alertname)
	}
	if !strings.HasPrefix(text, "# HighErrorRate\n- instance: prod (https://grafana.example.com)\n") {
		t.Errorf("want the Markdown header with the instance, got:\n%s", text)
	}
}

func TestDescribe_RequiresFingerprintAndInstance(t *testing.T) {
	in := instances(map[string]*fakeGrafana{"dev": {}}, "dev")
	if _, err := Describe(in, "dev", "", ""); err == nil {
		t.Error("a missing fingerprint should fail")
	}
	if _, err := Describe(in, "", "x", ""); err == nil {
		t.Error("a missing instance should fail with named instances")
	}
}
