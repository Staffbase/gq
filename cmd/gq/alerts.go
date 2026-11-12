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
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Staffbase/gq/internal/grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

const (
	defaultAlertLimit = 100
	maxAlertLimit     = 1000
)

// instanceFunc resolves an instance name ("" in single-instance mode) to a
// Querier.
type instanceFunc func(name string) (grafana.Querier, error)

// alertSource is where alerts come from: every registry instance, or the one
// Grafana of single-instance mode.
type alertSource struct {
	names []string // registry instance names; nil in single-instance mode
	get   instanceFunc
}

// multiInstance reports registry mode: several named instances.
func (s alertSource) multiInstance() bool { return len(s.names) > 0 }

// list asks the instance, or every instance at once when instance is "", for
// the alerts matching f. Grafana filters; a failing instance lands in the
// result's errors, unless no instance answered: then the call fails.
func (s alertSource) list(instance string, f grafana.AlertFilter, limit int) (alertList, error) {
	if err := f.Validate(); err != nil {
		return alertList{}, err
	}
	targets := s.names
	if instance != "" || !s.multiInstance() {
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
			q, err := s.get(name)
			if err == nil {
				results[i].alerts, err = q.ListAlerts(f)
			}
			results[i].err = err
		})
	}
	wg.Wait()

	if len(targets) == 1 && results[0].err != nil {
		return alertList{}, results[0].err
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
		return alertList{}, noInstanceAnswered(targets, errs)
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

// alert renders one alert of an instance as Markdown.
func (s alertSource) alert(instance, fingerprint, alertname string) (string, error) {
	if fingerprint == "" {
		return "", fmt.Errorf("fingerprint is required")
	}
	q, err := s.get(instance) // in registry mode, "" fails naming the instances
	if err != nil {
		return "", err
	}
	d, err := q.GetAlert(fingerprint, alertname)
	if err != nil {
		return "", err
	}
	return formatAlert(d, instance), nil
}

// registerAlertTools registers list_alerts and get_alert. Unlike the query
// tools, list_alerts takes an optional instance: without it, every instance
// is asked at once.
func registerAlertTools(s *server.MCPServer, src alertSource) {
	var listInstance, getInstance []mcp.ToolOption
	if src.multiInstance() {
		available := "Available: " + strings.Join(src.names, ", ")
		listInstance = append(listInstance, mcp.WithString("instance",
			mcp.Description("Grafana instance name; omit to ask every instance at once. "+available)))
		getInstance = append(getInstance, mcp.WithString("instance", mcp.Required(),
			mcp.Description("Grafana instance name, as returned by list_alerts. "+available)))
	}

	s.AddTool(
		mcp.NewTool("list_alerts",
			append(listInstance,
				mcp.WithDescription(
					"Query the alerts firing in Grafana Alerting, most severe and newest first. Grafana filters by the label matchers, "+
						"e.g. alertname=\"HighErrorRate\", namespace=\"shop\", team=\"payments\", severity=~\"critical|high\", "+
						"or the labels that name a cluster or environment. Labels all listed alerts share are in commonLabels once; "+
						"instances that could not be asked are in errors.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithArray("matchers", mcp.WithStringItems(),
					mcp.Description("Label matchers that must all match (operators =, !=, =~, !~), e.g. [\"namespace=\\\"shop\\\"\"].")),
				mcp.WithString("state", mcp.Enum(alertStateNames()...),
					mcp.Description("Alert state, active by default; suppressed means silenced or inhibited.")),
				mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max alerts to return (default %d, max %d); more counts the rest.", defaultAlertLimit, maxAlertLimit))),
			)...,
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := toolArgs(req)
			instance, _ := args["instance"].(string)
			matchers, err := stringList(args["matchers"])
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			state, _ := args["state"].(string)
			limit, err := intArg(args, "limit", defaultAlertLimit, maxAlertLimit)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			list, err := src.list(instance, grafana.AlertFilter{Matchers: matchers, State: grafana.AlertState(state)}, limit)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out, _ := json.Marshal(list)
			return mcp.NewToolResultText(string(out)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("get_alert",
			append(getInstance,
				mcp.WithDescription(
					"Get one alert as compact Markdown: header (instance, severity, state, start, summary, runbook, current values, labels), "+
						"description, the alert rule with its query and the gq tool that runs it, and links (dashboard, silence).",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithString("fingerprint", mcp.Required(), mcp.Description("Alert fingerprint, as returned by list_alerts.")),
				mcp.WithString("alertname", mcp.Description("The alert's alertname label; lets Grafana narrow the lookup. Pass it when known.")),
			)...,
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := toolArgs(req)
			instance, _ := args["instance"].(string)
			fingerprint, _ := args["fingerprint"].(string)
			alertname, _ := args["alertname"].(string)
			text, err := src.alert(instance, fingerprint, alertname)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(text), nil
		},
	)
}

// alertStateNames lists the alert states as strings, the default first.
func alertStateNames() []string {
	names := make([]string, len(grafana.AlertStates))
	for i, st := range grafana.AlertStates {
		names[i] = string(st)
	}
	return names
}

// stringList reads a JSON array of strings from a tool argument.
func stringList(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("matchers must be an array of strings")
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, fmt.Errorf("matchers must be an array of strings")
		}
		out = append(out, s)
	}
	return out, nil
}

func buildAlertsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "Query the alerts firing in Grafana, filtered by Grafana",
		Long: "Query the alerts firing in Grafana Alerting. Grafana filters by the label matchers, e.g.\n" +
			"  gq alerts -m 'namespace=\"shop\"' -m 'severity=~\"critical|high\"'\n" +
			"Without --instance, every instance of a multi-instance config is asked at once.",
		RunE: runAlerts,
	}
	cmd.Flags().StringArrayP("matcher", "m", nil, "Label matcher that must match (repeatable; =, !=, =~, !~)")
	cmd.Flags().String("state", string(grafana.StateActive), strings.Join(alertStateNames(), ", "))
	cmd.Flags().Int("limit", defaultAlertLimit, "Max alerts to print")
	cmd.Flags().String("instance", "", "Grafana instance name (registry mode); default: all instances")
	return cmd
}

func buildAlertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alert <fingerprint>",
		Short: "Show one alert: header, description, rule query and links",
		Args:  cobra.ExactArgs(1),
		RunE:  runAlert,
	}
	cmd.Flags().String("alertname", "", "The alert's alertname label; lets Grafana narrow the lookup")
	cmd.Flags().String("instance", "", "Grafana instance name (registry mode)")
	return cmd
}

// cliAlertSource lists every registry instance when GRAFANA_CONFIG holds a
// registry; otherwise it is single-instance mode.
func cliAlertSource() (alertSource, error) {
	src := alertSource{get: func(name string) (grafana.Querier, error) { return newQuerier(name) }}
	reg, err := registryFromEnv()
	if err != nil {
		return src, err
	}
	if reg != nil {
		src.names = reg.InstanceNames()
	}
	return src, nil
}

func runAlerts(cmd *cobra.Command, _ []string) error {
	matchers, _ := cmd.Flags().GetStringArray("matcher")
	state, _ := cmd.Flags().GetString("state")
	limit, _ := cmd.Flags().GetInt("limit")
	instance, _ := cmd.Flags().GetString("instance")
	if limit <= 0 || limit > maxAlertLimit {
		return fmt.Errorf("--limit must be between 1 and %d", maxAlertLimit)
	}
	src, err := cliAlertSource()
	if err != nil {
		return err
	}
	list, err := src.list(instance, grafana.AlertFilter{Matchers: matchers, State: grafana.AlertState(state)}, limit)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return err
}

func runAlert(cmd *cobra.Command, args []string) error {
	alertname, _ := cmd.Flags().GetString("alertname")
	instance, _ := cmd.Flags().GetString("instance")
	src, err := cliAlertSource()
	if err != nil {
		return err
	}
	text, err := src.alert(instance, args[0], alertname)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), text)
	return err
}
