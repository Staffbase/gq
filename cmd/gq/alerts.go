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

	"github.com/Staffbase/gq/internal/alerts"
	"github.com/Staffbase/gq/internal/grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

// registerAlertTools registers list_alerts and get_alert. Unlike the query
// tools, list_alerts takes an optional instance: without it, every instance
// is asked at once.
func registerAlertTools(s *server.MCPServer, in grafana.Instances) {
	var listInstance, getInstance []mcp.ToolOption
	if in.Named() {
		available := "Available: " + strings.Join(in.Names, ", ")
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
				mcp.WithString("state", mcp.Enum(alerts.StateNames()...),
					mcp.Description("Alert state, active by default; suppressed means silenced or inhibited.")),
				mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max alerts to return (default %d, max %d); more counts the rest.", alerts.DefaultLimit, alerts.MaxLimit))),
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
			limit, err := intArg(args, "limit", alerts.DefaultLimit, alerts.MaxLimit)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			list, err := alerts.List(in, instance, grafana.AlertFilter{Matchers: matchers, State: grafana.AlertState(state)}, limit)
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
			text, err := alerts.Describe(in, instance, fingerprint, alertname)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(text), nil
		},
	)
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
	cmd.Flags().String("state", string(grafana.StateActive), strings.Join(alerts.StateNames(), ", "))
	cmd.Flags().Int("limit", alerts.DefaultLimit, "Max alerts to print")
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

// cliInstances are the registry's instances when GRAFANA_CONFIG holds a
// registry, otherwise the one unnamed instance. newQuerier creates the clients.
func cliInstances() (grafana.Instances, error) {
	reg, err := registryFromEnv()
	if err != nil {
		return grafana.Instances{}, err
	}
	names := []string{""}
	if reg != nil {
		names = reg.InstanceNames()
	}
	return grafana.Instances{Names: names, Get: newQuerier}, nil
}

func runAlerts(cmd *cobra.Command, _ []string) error {
	matchers, _ := cmd.Flags().GetStringArray("matcher")
	state, _ := cmd.Flags().GetString("state")
	limit, _ := cmd.Flags().GetInt("limit")
	instance, _ := cmd.Flags().GetString("instance")
	if limit <= 0 || limit > alerts.MaxLimit {
		return fmt.Errorf("--limit must be between 1 and %d", alerts.MaxLimit)
	}
	in, err := cliInstances()
	if err != nil {
		return err
	}
	list, err := alerts.List(in, instance, grafana.AlertFilter{Matchers: matchers, State: grafana.AlertState(state)}, limit)
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
	in, err := cliInstances()
	if err != nil {
		return err
	}
	text, err := alerts.Describe(in, instance, args[0], alertname)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), text)
	return err
}
