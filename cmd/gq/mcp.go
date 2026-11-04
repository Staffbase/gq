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
	"os"
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
	s := server.NewMCPServer("gq", version, server.WithToolCapabilities(false))

	reg, err := registryFromEnv()
	if err != nil {
		return err
	}

	if reg != nil {
		// Registry mode: instance param required on every tool.
		cache := newClientCache(reg)
		registerTools(s, cache.querierFor,
			mcp.WithString("instance", mcp.Required(),
				mcp.Description("Grafana instance name. Available: "+strings.Join(reg.InstanceNames(), ", ")),
			),
		)
		return server.ServeStdio(s)
	}

	// Single-instance mode: env vars or a single-instance config file.
	client, err := grafana.NewClientFromEnv()
	if err != nil {
		return err
	}
	registerTools(s,
		func(_ map[string]any) (grafana.Querier, error) { return client, nil },
	)
	return server.ServeStdio(s)
}

// registryFromEnv returns the registry GRAFANA_CONFIG points at, or nil when
// GRAFANA_CONFIG is unset or holds a single-instance config.
//
// A GRAFANA_CONFIG that is set but unreadable is an error, not a fall-through to
// single-instance mode: reporting "GRAFANA_URL is required" for what is really a
// malformed config file sends people looking in the wrong place.
func registryFromEnv() (*grafana.Registry, error) {
	if os.Getenv("GRAFANA_CONFIG") == "" {
		return nil, nil
	}
	reg, _, err := grafana.LoadConfigFileFromEnv()
	if err != nil {
		return nil, err
	}
	return reg, nil
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

// querierFor resolves the "instance" tool argument to a cached client.
func (cc *clientCache) querierFor(args map[string]any) (grafana.Querier, error) {
	instance, _ := args["instance"].(string)
	if instance == "" {
		return nil, fmt.Errorf("instance is required (available: %s)", strings.Join(cc.reg.InstanceNames(), ", "))
	}

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

// registerTools registers all gq tools on s.
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

	s.AddTool(
		mcp.NewTool("query_traces",
			append(extraParams,
				mcp.WithDescription(
					"Search traces in VictoriaTraces using TraceQL via Grafana proxy. Returns one headline per trace: "+
						"trace ID, root service/operation, start time, duration. Use query_trace_by_id for the spans and service map. "+
						"Example: '{ resource.service.name = \"frontend\" && status = error }'",
				),
				mcp.WithString("query", mcp.Required(), mcp.Description("TraceQL query expression.")),
				mcp.WithString("start", mcp.Description("Start time: RFC3339, Unix timestamp, or relative (default: now-1h).")),
				mcp.WithString("end", mcp.Description("End time (default: now).")),
				mcp.WithNumber("limit", mcp.Description("Max traces to return (default 20).")),
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
			if start == "" {
				start = "now-1h"
			}
			if end == "" {
				end = "now"
			}
			limit := 20
			if l, ok := args["limit"].(float64); ok {
				if l <= 0 || l != float64(int(l)) || l > 1000 {
					return mcp.NewToolResultError("limit must be a positive integer not exceeding 1000"), nil
				}
				limit = int(l)
			}
			hits, err := q.SearchTraces(query, start, end, limit)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out, _ := json.MarshalIndent(hits, "", "  ")
			return mcp.NewToolResultText(string(out)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("query_trace_by_id",
			append(extraParams,
				mcp.WithDescription(
					"Fetch one trace from VictoriaTraces by trace ID. Returns the summary (root span, duration, "+
						"error count, service map of caller→callee) plus every span with parent, service, operation, offset and duration.",
				),
				mcp.WithString("trace_id", mcp.Required(), mcp.Description("Trace ID (hex).")),
			)...,
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := toolArgs(req)
			q, err := getClient(args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			id, _ := args["trace_id"].(string)
			if id == "" {
				return mcp.NewToolResultError("trace_id is required"), nil
			}
			sum, err := q.GetTrace(id)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out, _ := json.MarshalIndent(sum, "", "  ")
			return mcp.NewToolResultText(string(out)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("query_profile_types",
			append(extraParams,
				mcp.WithDescription(
					"List the profile types available in Pyroscope (CPU, memory, ...). "+
						"Pass an ID as profile_type to query_profile.",
				),
			)...,
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			q, err := getClient(toolArgs(req))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			types, err := q.ProfileTypes()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out, _ := json.MarshalIndent(types, "", "  ")
			return mcp.NewToolResultText(string(out)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("query_profile_label_values",
			append(extraParams,
				mcp.WithDescription(
					"List values of a Pyroscope profile label. Use label='service_name' to discover which services have profiles.",
				),
				mcp.WithString("label", mcp.Description("Label name (default: service_name).")),
				mcp.WithString("start", mcp.Description("Start time (default: now-1h).")),
				mcp.WithString("end", mcp.Description("End time (default: now).")),
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
				label = "service_name"
			}
			start, end := timeRange(args)
			values, err := q.ProfileLabelValues(label, start, end)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out, _ := json.MarshalIndent(values, "", "  ")
			return mcp.NewToolResultText(string(out)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("query_profile",
			append(extraParams,
				mcp.WithDescription(
					"Query merged profiling data from Pyroscope via Grafana proxy. Returns total cost and the top functions "+
						"by self cost (hot spots) with their share. 'other' is Pyroscope's bucket for pruned small nodes; a shorter time range prunes less. Example selector: '{service_name=\"backend\"}'",
				),
				mcp.WithString("selector", mcp.Required(), mcp.Description("Label selector, e.g. {service_name=\"backend\"}.")),
				mcp.WithString("profile_type", mcp.Description("Profile type ID from query_profile_types (default: CPU).")),
				mcp.WithString("start", mcp.Description("Start time (default: now-1h).")),
				mcp.WithString("end", mcp.Description("End time (default: now).")),
				mcp.WithNumber("limit", mcp.Description("Number of top functions to return (default 20).")),
			)...,
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := toolArgs(req)
			q, err := getClient(args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			selector, _ := args["selector"].(string)
			if selector == "" {
				return mcp.NewToolResultError("selector is required"), nil
			}
			ptype, _ := args["profile_type"].(string)
			if ptype == "" {
				ptype = defaultProfileType
			}
			limit := 20
			if l, ok := args["limit"].(float64); ok {
				if l <= 0 || l != float64(int(l)) || l > 500 {
					return mcp.NewToolResultError("limit must be a positive integer not exceeding 500"), nil
				}
				limit = int(l)
			}
			start, end := timeRange(args)
			sum, err := q.SelectProfile(ptype, selector, start, end, limit)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out, _ := json.MarshalIndent(sum, "", "  ")
			return mcp.NewToolResultText(string(out)), nil
		},
	)
}

const defaultProfileType = "process_cpu:cpu:nanoseconds:cpu:nanoseconds"

// timeRange reads start/end tool args, defaulting to the last hour.
func timeRange(args map[string]any) (start, end string) {
	start, _ = args["start"].(string)
	end, _ = args["end"].(string)
	if start == "" {
		start = "now-1h"
	}
	if end == "" {
		end = "now"
	}
	return start, end
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
