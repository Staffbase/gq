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
	"os"

	"github.com/Staffbase/gq/internal/grafana"
	"github.com/spf13/cobra"
)

// newQuerier is the factory used by all run* functions.
// Overridden in tests to inject a fake grafana.Querier.
var newQuerier func() (grafana.Querier, error) = func() (grafana.Querier, error) {
	return grafana.NewClientFromEnv()
}

func main() {
	if err := buildRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func buildRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "gq",
		Short: "Query logs and metrics via Grafana datasource proxy",
	}
	root.AddCommand(
		buildQueryCmd(),
		buildMetricsCmd(),
		buildInstantCmd(),
		buildMCPCmd(),
	)
	return root
}

func buildQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Run a LogsQL query against VictoriaLogs",
		RunE:  runQuery,
	}
	cmd.Flags().StringP("query", "q", "", "LogsQL query (required)")
	cmd.Flags().String("start", "", "Start time: RFC3339, Unix timestamp, or relative (e.g. '1h', 'now-30m')")
	cmd.Flags().String("end", "", "End time")
	cmd.Flags().Int("limit", 100, "Max number of log lines to return")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func buildMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Run a PromQL range query against VictoriaMetrics",
		RunE:  runMetrics,
	}
	cmd.Flags().StringP("query", "q", "", "PromQL query (required)")
	cmd.Flags().String("start", "now-1h", "Start time")
	cmd.Flags().String("end", "now", "End time")
	cmd.Flags().String("step", "60s", "Query resolution step (e.g. 60s, 5m, 1h)")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func buildInstantCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instant",
		Short: "Run a PromQL instant query against VictoriaMetrics",
		RunE:  runInstant,
	}
	cmd.Flags().StringP("query", "q", "", "PromQL query (required)")
	cmd.Flags().String("time", "", "Evaluation time (RFC3339 or Unix timestamp, defaults to now)")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func runQuery(cmd *cobra.Command, _ []string) error {
	q, err := newQuerier()
	if err != nil {
		return err
	}
	query, _ := cmd.Flags().GetString("query")
	start, _ := cmd.Flags().GetString("start")
	end, _ := cmd.Flags().GetString("end")
	limit, _ := cmd.Flags().GetInt("limit")
	out, err := q.QueryLogs(query, start, end, limit)
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func runMetrics(cmd *cobra.Command, _ []string) error {
	q, err := newQuerier()
	if err != nil {
		return err
	}
	query, _ := cmd.Flags().GetString("query")
	start, _ := cmd.Flags().GetString("start")
	end, _ := cmd.Flags().GetString("end")
	step, _ := cmd.Flags().GetString("step")
	raw, err := q.QueryMetricsRange(query, start, end, step)
	if err != nil {
		return err
	}
	prettyPrint(raw)
	return nil
}

func runInstant(cmd *cobra.Command, _ []string) error {
	q, err := newQuerier()
	if err != nil {
		return err
	}
	query, _ := cmd.Flags().GetString("query")
	t, _ := cmd.Flags().GetString("time")
	raw, err := q.QueryMetricsInstant(query, t)
	if err != nil {
		return err
	}
	prettyPrint(raw)
	return nil
}

func prettyPrint(raw []byte) {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		fmt.Println(string(raw))
		return
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}
