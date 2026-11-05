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
	"fmt"

	"github.com/spf13/cobra"
)

func buildTracesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "traces",
		Short: "Search traces in VictoriaTraces with TraceQL",
		Long: "Search traces in VictoriaTraces with TraceQL, e.g. '{ resource.service.name = \"frontend\" && status = error }'.\n" +
			"Wide time ranges are slow; keep --start close to now.",
		RunE: runTraces,
	}
	cmd.Flags().StringP("query", "q", "", "TraceQL query (required)")
	cmd.Flags().String("start", "now-1h", "Start time")
	cmd.Flags().String("end", "now", "End time")
	cmd.Flags().Int("limit", 20, "Max number of traces to return")
	cmd.Flags().String("instance", "", "Grafana instance name (registry mode)")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func buildTraceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace <trace-id>",
		Short: "Show one trace: summary, service map and spans",
		Args:  cobra.ExactArgs(1),
		RunE:  runTrace,
	}
	cmd.Flags().String("instance", "", "Grafana instance name (registry mode)")
	return cmd
}

func buildProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Show the top functions by self cost from Pyroscope",
		RunE:  runProfile,
	}
	cmd.Flags().StringP("selector", "s", "", `Label selector, e.g. {service_name="backend"} (required)`)
	cmd.Flags().String("type", defaultProfileType, "Profile type ID (see profile-types)")
	cmd.Flags().String("start", "now-1h", "Start time")
	cmd.Flags().String("end", "now", "End time")
	cmd.Flags().Int("limit", 20, "Number of top functions to return")
	cmd.Flags().String("instance", "", "Grafana instance name (registry mode)")
	_ = cmd.MarkFlagRequired("selector")
	return cmd
}

func buildProfileTypesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile-types",
		Short: "List the profile types available in Pyroscope",
		RunE:  runProfileTypes,
	}
	cmd.Flags().String("instance", "", "Grafana instance name (registry mode)")
	return cmd
}

func buildProfileLabelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile-labels",
		Short: "List values of a Pyroscope label, e.g. the profiled services",
		RunE:  runProfileLabels,
	}
	cmd.Flags().String("label", "service_name", "Label name")
	cmd.Flags().String("start", "now-1h", "Start time")
	cmd.Flags().String("end", "now", "End time")
	cmd.Flags().String("instance", "", "Grafana instance name (registry mode)")
	return cmd
}

func runTraces(cmd *cobra.Command, _ []string) error {
	instance, _ := cmd.Flags().GetString("instance")
	q, err := newQuerier(instance)
	if err != nil {
		return err
	}
	query, _ := cmd.Flags().GetString("query")
	start, _ := cmd.Flags().GetString("start")
	end, _ := cmd.Flags().GetString("end")
	limit, _ := cmd.Flags().GetInt("limit")
	hits, err := q.SearchTraces(query, start, end, limit)
	if err != nil {
		return err
	}
	return printJSON(hits)
}

func runTrace(cmd *cobra.Command, args []string) error {
	instance, _ := cmd.Flags().GetString("instance")
	q, err := newQuerier(instance)
	if err != nil {
		return err
	}
	trace, err := q.GetTrace(args[0])
	if err != nil {
		return err
	}
	return printJSON(trace)
}

func runProfile(cmd *cobra.Command, _ []string) error {
	instance, _ := cmd.Flags().GetString("instance")
	q, err := newQuerier(instance)
	if err != nil {
		return err
	}
	selector, _ := cmd.Flags().GetString("selector")
	profileType, _ := cmd.Flags().GetString("type")
	start, _ := cmd.Flags().GetString("start")
	end, _ := cmd.Flags().GetString("end")
	limit, _ := cmd.Flags().GetInt("limit")
	profile, err := q.SelectProfile(profileType, selector, start, end, limit)
	if err != nil {
		return err
	}
	return printJSON(profile)
}

func runProfileTypes(cmd *cobra.Command, _ []string) error {
	instance, _ := cmd.Flags().GetString("instance")
	q, err := newQuerier(instance)
	if err != nil {
		return err
	}
	types, err := q.ProfileTypes()
	if err != nil {
		return err
	}
	return printJSON(types)
}

func runProfileLabels(cmd *cobra.Command, _ []string) error {
	instance, _ := cmd.Flags().GetString("instance")
	q, err := newQuerier(instance)
	if err != nil {
		return err
	}
	label, _ := cmd.Flags().GetString("label")
	start, _ := cmd.Flags().GetString("start")
	end, _ := cmd.Flags().GetString("end")
	values, err := q.ProfileLabelValues(label, start, end)
	if err != nil {
		return err
	}
	return printJSON(values)
}

func printJSON(v any) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
