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

func buildMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP server (stdio transport)",
		RunE:  runMCP,
	}
}

// clientFunc resolves a Querier from a tool call's arguments.
// In single-instance mode it ignores args and returns the fixed client.
// In registry mode it reads "instance" from args and looks up the cache.
type clientFunc func(args map[string]any) (grafana.Querier, error)

func runMCP(_ *cobra.Command, _ []string) error {
	s := server.NewMCPServer("gq", "0.1.0", server.WithToolCapabilities(false))

	reg, _, err := grafana.LoadConfigFileFromEnv()
	if err == nil && reg != nil {
		// Registry mode: instance param required on every tool.
		cache := newClientCache(reg)
		registerTools(s,
			func(args map[string]any) (grafana.Querier, error) {
				inst, _ := args["instance"].(string)
				if inst == "" {
					return nil, fmt.Errorf("instance is required")
				}
				return cache.get(inst)
			},
			mcp.WithString("instance", mcp.Required(),
				mcp.Description("Grafana instance name. Available: "+strings.Join(reg.InstanceNames(), ", ")),
			),
		)
		return server.ServeStdio(s)
	}

	// Single-instance mode: fall back to env vars / single-instance config.
	client, err := grafana.NewClientFromEnv()
	if err != nil {
		return err
	}
	registerTools(s,
		func(_ map[string]any) (grafana.Querier, error) { return client, nil },
	)
	return server.ServeStdio(s)
}

// clientCache caches per-instance clients within a session so refreshed tokens
// (updated on 401) are preserved across calls.
type clientCache struct {
	mu      sync.Mutex
	reg     *grafana.Registry
	clients map[string]*grafana.Client
}

func newClientCache(reg *grafana.Registry) *clientCache {
	return &clientCache{reg: reg, clients: make(map[string]*grafana.Client)}
}

func (cc *clientCache) get(instance string) (grafana.Querier, error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if c, ok := cc.clients[instance]; ok {
		return c, nil
	}
	inst, err := cc.reg.ResolvedInstance(instance)
	if err != nil {
		return nil, err
	}
	c := grafana.NewClientFromInstance(inst)
	cc.clients[instance] = c
	return c, nil
}

// registerTools registers all four gq tools on s.
// getClient is called per-request to resolve the Querier.
// extraParams are prepended to every tool's parameter list (e.g. "instance").
func registerTools(s *server.MCPServer, getClient clientFunc, extraParams ...mcp.ToolOption) {
	s.AddTool(
		mcp.NewTool("query_logs",
			append(extraParams,
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
			)...,
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := toolArgs(req)
			q, err := getClient(args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
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
			append(extraParams,
				mcp.WithDescription(
					"Run a PromQL range query against VictoriaMetrics via Grafana proxy. Returns time series data. "+
						"Example: 'flink_jobmanager_job_numRestarts{namespace=\"flink\"}'",
				),
				mcp.WithString("query", mcp.Required(), mcp.Description("PromQL query expression.")),
				mcp.WithString("start", mcp.Description("Start time (default: now-1h).")),
				mcp.WithString("end", mcp.Description("End time (default: now).")),
				mcp.WithString("step", mcp.Description("Resolution step (default: 60s).")),
			)...,
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := toolArgs(req)
			q, err := getClient(args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
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
			append(extraParams,
				mcp.WithDescription(
					"Run a PromQL instant query against VictoriaMetrics — returns current values at a single point in time. "+
						"Example: 'flink_jobmanager_job_numRestarts{namespace=\"flink\"}'",
				),
				mcp.WithString("query", mcp.Required(), mcp.Description("PromQL query expression.")),
				mcp.WithString("time", mcp.Description("Evaluation time (RFC3339 or Unix timestamp). Defaults to now.")),
			)...,
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := toolArgs(req)
			q, err := getClient(args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
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
			append(extraParams,
				mcp.WithDescription(
					"List distinct values for a given metric label in VictoriaMetrics. "+
						"Useful for discovering namespaces, jobs, pods, or services before querying. "+
						"Example: label='namespace'",
				),
				mcp.WithString("label", mcp.Required(),
					mcp.Description("Label name to list values for. Examples: namespace, job, pod, container."),
				),
				mcp.WithString("match", mcp.Description("Optional PromQL selector to narrow results.")),
			)...,
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := toolArgs(req)
			q, err := getClient(args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
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

func toolArgs(req mcp.CallToolRequest) map[string]any {
	if args, ok := req.Params.Arguments.(map[string]any); ok {
		return args
	}
	return map[string]any{}
}

func prettyJSON(raw []byte) string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}
