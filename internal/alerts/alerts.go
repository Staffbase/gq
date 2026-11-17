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

// Package alerts queries the alerts firing in Grafana Alerting across gq's
// Grafana instances and renders them for agents: a compact list and one alert
// as Markdown.
package alerts

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Staffbase/gq/internal/grafana"
)

// Limits on the number of alerts List returns.
const (
	DefaultLimit = 100
	MaxLimit     = 1000
)

// List asks the instance, or every instance at once when instance is "", for
// the alerts matching f, and returns the first limit of them. Grafana filters;
// a failing instance lands in the result's Errors, unless no instance
// answered: then List fails.
func List(in grafana.Instances, instance string, f grafana.AlertFilter, limit int) (Result, error) {
	if err := f.Validate(); err != nil {
		return Result{}, err
	}
	targets := in.Names
	if instance != "" {
		targets = []string{instance}
	}
	type result struct {
		alerts []grafana.Alert
		err    error
	}
	results := make([]result, len(targets))
	var wg sync.WaitGroup
	for i, name := range targets {
		wg.Go(func() {
			q, err := in.Get(name)
			if err == nil {
				results[i].alerts, err = q.ListAlerts(f)
			}
			results[i].err = err
		})
	}
	wg.Wait()

	if len(targets) == 1 && results[0].err != nil {
		return Result{}, results[0].err
	}
	var all []instanceAlert
	errs := map[string]string{}
	for i, name := range targets {
		if results[i].err != nil {
			errs[name] = results[i].err.Error()
			continue
		}
		for _, a := range results[i].alerts {
			all = append(all, instanceAlert{Instance: name, Alert: a})
		}
	}
	if len(errs) == len(targets) {
		return Result{}, noInstanceAnswered(targets, errs)
	}
	return compactAlerts(all, errs, limit), nil
}

// noInstanceAnswered names each distinct error once, with the instances that
// got it: a bad matcher fails every instance the same way.
func noInstanceAnswered(targets []string, errs map[string]string) error {
	var msgs []string
	byMsg := map[string][]string{}
	for _, name := range targets {
		msg := errs[name]
		if byMsg[msg] == nil {
			msgs = append(msgs, msg)
		}
		byMsg[msg] = append(byMsg[msg], name)
	}
	parts := make([]string, len(msgs))
	for i, msg := range msgs {
		parts[i] = strings.Join(byMsg[msg], ", ") + ": " + msg
	}
	return fmt.Errorf("no instance answered: %s", strings.Join(parts, "; "))
}

// Describe renders one alert of an instance as Markdown. alertname is
// optional; it lets Grafana narrow the lookup.
func Describe(in grafana.Instances, instance, fingerprint, alertname string) (string, error) {
	if fingerprint == "" {
		return "", fmt.Errorf("fingerprint is required")
	}
	q, err := in.Get(instance) // with named instances, "" fails listing them
	if err != nil {
		return "", err
	}
	d, err := q.GetAlert(fingerprint, alertname)
	if err != nil {
		return "", err
	}
	return formatAlert(d, instance), nil
}

// StateNames lists the alert states as strings, the default first.
func StateNames() []string {
	names := make([]string, len(grafana.AlertStates))
	for i, st := range grafana.AlertStates {
		names[i] = string(st)
	}
	return names
}
