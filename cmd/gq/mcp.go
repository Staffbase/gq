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

	"github.com/Staffbase/gq/internal/grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func buildMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP server (stdio transport)",
		RunE:  runMCP,
	}
}

func runMCP(_ *cobra.Command, _ []string) error {
	client, err := grafana.NewClientFromEnv()
	if err != nil {
		return err
	}
	s := server.NewMCPServer("gq", "0.1.0", server.WithToolCapabilities(false))
	registerTools(s, client)
	return server.ServeStdio(s)
}

func registerTools(s *server.MCPServer, q grafana.Querier) {
	s.AddTool(
		mcp.NewTool("query_logs",
			mcp.WithDescription(
				"Query VictoriaLogs using LogsQL syntax via Grafana proxy. Returns NDJSON log entries. "+
					"Example queries: 'k8s.namespace.name:flink severity:ERROR _time:1h', "+
					"'service.name:my-service _msg:timeout _time:30m'",
			),
			mcp.WithString("query", mcp.Required(),
				mcp.Description("LogsQL query expression. Use field:value syntax for filtering, _time:Xm for time range."),
			),
			mcp.WithString("start", mcp.Description("Start time: RFC3339, Unix timestamp, or relative (e.g. '1h', 'now-30m').")),
			mcp.WithString("end", mcp.Description("End time: RFC3339, Unix timestamp, or relative (e.g. 'now', 'now+5m').")),
			mcp.WithNumber("limit", mcp.Description("Max log lines to return (default 100).")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, ok := req.Params.Arguments.(map[string]any)
			if !ok {
				return mcp.NewToolResultError("invalid arguments: expected object"), nil
			}
			query, _ := args["query"].(string)
			if query == "" {
				return mcp.NewToolResultError("query is required"), nil
			}
			start, _ := args["start"].(string)
			end, _ := args["end"].(string)
			limit := 100
			if l, ok := args["limit"].(float64); ok {
				if l <= 0 || l != float64(int(l)) || l > 10000 {
					return mcp.NewToolResultError("limit must be a positive integer not exceeding 10000"), nil
				}
				limit = int(l)
			}
			out, err := q.QueryLogs(query, start, end, limit)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(out)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("query_metrics",
			mcp.WithDescription(
				"Run a PromQL range query against VictoriaMetrics via Grafana proxy. Returns time series data. "+
					"Example: 'flink_jobmanager_job_numRestarts{namespace=\"flink\"}'",
			),
			mcp.WithString("query", mcp.Required(), mcp.Description("PromQL query expression.")),
			mcp.WithString("start", mcp.Description("Start time (default: now-1h).")),
			mcp.WithString("end", mcp.Description("End time (default: now).")),
			mcp.WithString("step", mcp.Description("Resolution step (default: 60s).")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, ok := req.Params.Arguments.(map[string]any)
			if !ok {
				return mcp.NewToolResultError("invalid arguments: expected object"), nil
			}
			query, _ := args["query"].(string)
			if query == "" {
				return mcp.NewToolResultError("query is required"), nil
			}
			start, _ := args["start"].(string)
			end, _ := args["end"].(string)
			step, _ := args["step"].(string)
			if start == "" {
				start = "now-1h"
			}
			if end == "" {
				end = "now"
			}
			if step == "" {
				step = "60s"
			}
			raw, err := q.QueryMetricsRange(query, start, end, step)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(prettyJSON(raw)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("query_metrics_instant",
			mcp.WithDescription(
				"Run a PromQL instant query against VictoriaMetrics — returns current values at a single point in time. "+
					"Example: 'flink_jobmanager_job_numRestarts{namespace=\"flink\"}'",
			),
			mcp.WithString("query", mcp.Required(), mcp.Description("PromQL query expression.")),
			mcp.WithString("time", mcp.Description("Evaluation time (RFC3339 or Unix timestamp). Defaults to now.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, ok := req.Params.Arguments.(map[string]any)
			if !ok {
				return mcp.NewToolResultError("invalid arguments: expected object"), nil
			}
			query, _ := args["query"].(string)
			if query == "" {
				return mcp.NewToolResultError("query is required"), nil
			}
			t, _ := args["time"].(string)
			raw, err := q.QueryMetricsInstant(query, t)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(prettyJSON(raw)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("list_label_values",
			mcp.WithDescription(
				"List distinct values for a given metric label in VictoriaMetrics. "+
					"Useful for discovering namespaces, jobs, pods, or services before querying. "+
					"Example: label='namespace'",
			),
			mcp.WithString("label", mcp.Required(),
				mcp.Description("Label name to list values for. Examples: namespace, job, pod, container."),
			),
			mcp.WithString("match", mcp.Description("Optional PromQL selector to narrow results.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, ok := req.Params.Arguments.(map[string]any)
			if !ok {
				return mcp.NewToolResultError("invalid arguments: expected object"), nil
			}
			label, _ := args["label"].(string)
			if label == "" {
				return mcp.NewToolResultError("label is required"), nil
			}
			match, _ := args["match"].(string)
			values, err := q.ListLabelValues(label, match)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out, _ := json.MarshalIndent(values, "", "  ")
			return mcp.NewToolResultText(string(out)), nil
		},
	)
}

func prettyJSON(raw []byte) string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}
