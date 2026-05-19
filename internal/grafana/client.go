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

// Package grafana provides a minimal HTTP client for querying VictoriaLogs
// and VictoriaMetrics through the Grafana datasource proxy.
package grafana

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// defaultHTTPTimeout bounds every request the Client makes so a slow or
// unreachable Grafana cannot hang the CLI or MCP server indefinitely.
const defaultHTTPTimeout = 30 * time.Second

// Querier is the interface implemented by Client.
// Both the gq CLI and the `gq mcp` server depend on this interface;
// tests inject a fake implementation.
type Querier interface {
	QueryLogs(query, start, end string, limit int) ([]byte, error)
	QueryMetricsRange(query, start, end, step string) ([]byte, error)
	QueryMetricsInstant(query, t string) ([]byte, error)
	ListLabelValues(label, match string) ([]string, error)
}

// Client holds connection config for the Grafana proxy.
type Client struct {
	BaseURL string
	// Cookie is the raw Grafana session cookie (e.g. "grafana_session=abc123").
	// Takes precedence over Token if both are set.
	Cookie string
	// Token is a Grafana service account or API token for Bearer auth.
	Token string
	// LogsDatasourceUID is the Grafana datasource UID for VictoriaLogs.
	LogsDatasourceUID string
	// MetricsDatasourceUID is the Grafana datasource UID for VictoriaMetrics.
	MetricsDatasourceUID string
	// HTTPClient is optional; when nil a client with defaultHTTPTimeout is used.
	HTTPClient *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: defaultHTTPTimeout}
}

// NewClientFromEnv constructs a Client using the following precedence:
//  1. GRAFANA_CONFIG env var → load config file (token auth)
//  2. GRAFANA_URL + (GRAFANA_SERVICE_ACCOUNT_TOKEN | GRAFANA_COOKIE) env vars
//
// The env var name GRAFANA_SERVICE_ACCOUNT_TOKEN matches grafana/mcp-grafana's
// convention so a single env var works across gq, mcp-grafana, and any future
// MCP server wrapped via gq-auth.
//
// Returns an error if no valid configuration is found.
func NewClientFromEnv() (*Client, error) {
	// 1. Config file path from env.
	if cfgPath := os.Getenv("GRAFANA_CONFIG"); cfgPath != "" {
		return NewClientFromConfig(cfgPath)
	}

	// 2. Direct env vars.
	baseURL := os.Getenv("GRAFANA_URL")
	if baseURL == "" {
		return nil, fmt.Errorf("GRAFANA_URL environment variable is required (or set GRAFANA_CONFIG)")
	}
	cookie := os.Getenv("GRAFANA_COOKIE")
	token := os.Getenv("GRAFANA_SERVICE_ACCOUNT_TOKEN")
	if cookie == "" && token == "" {
		return nil, fmt.Errorf("either GRAFANA_SERVICE_ACCOUNT_TOKEN or GRAFANA_COOKIE environment variable is required")
	}
	return &Client{
		BaseURL:              strings.TrimRight(baseURL, "/"),
		Cookie:               cookie,
		Token:                token,
		LogsDatasourceUID:    "victorialogs",
		MetricsDatasourceUID: "victoriametrics",
	}, nil
}

// do executes an HTTP request, injecting auth headers.
func (c *Client) do(method, endpoint string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Cookie != "" {
		req.Header.Set("Cookie", c.Cookie)
	} else if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return c.httpClient().Do(req)
}

// QueryLogs runs a LogsQL query against VictoriaLogs via the Grafana proxy.
// Returns raw NDJSON bytes.
func (c *Client) QueryLogs(query, start, end string, limit int) ([]byte, error) {
	form := url.Values{}
	form.Set("query", query)
	if start != "" {
		form.Set("start", start)
	}
	if end != "" {
		form.Set("end", end)
	}
	if limit > 0 {
		form.Set("limit", fmt.Sprintf("%d", limit))
	}

	endpoint := c.BaseURL + "/api/datasources/proxy/uid/" + c.LogsDatasourceUID + "/select/logsql/query"
	resp, err := c.do("POST", endpoint, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, readBody(resp.Body))
	}

	// Read all NDJSON lines, validate each, return as newline-joined bytes.
	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// QueryMetricsRange runs a PromQL range query against VictoriaMetrics via the Grafana proxy.
// Returns the raw JSON response body.
func (c *Client) QueryMetricsRange(query, start, end, step string) ([]byte, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", start)
	params.Set("end", end)
	params.Set("step", step)

	endpoint := c.BaseURL + "/api/datasources/proxy/uid/" + c.MetricsDatasourceUID + "/api/v1/query_range?" + params.Encode()
	resp, err := c.do("GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, readBody(resp.Body))
	}
	return io.ReadAll(resp.Body)
}

// QueryMetricsInstant runs a PromQL instant query against VictoriaMetrics.
// Returns the raw JSON response body.
func (c *Client) QueryMetricsInstant(query, t string) ([]byte, error) {
	params := url.Values{}
	params.Set("query", query)
	if t != "" {
		params.Set("time", t)
	}

	endpoint := c.BaseURL + "/api/datasources/proxy/uid/" + c.MetricsDatasourceUID + "/api/v1/query?" + params.Encode()
	resp, err := c.do("GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, readBody(resp.Body))
	}
	return io.ReadAll(resp.Body)
}

// ListLabelValues returns distinct values for a given metric label.
// An optional PromQL match[] selector can be used to narrow results.
func (c *Client) ListLabelValues(label, match string) ([]string, error) {
	params := url.Values{}
	if match != "" {
		params.Set("match[]", match)
	}

	endpoint := c.BaseURL + "/api/datasources/proxy/uid/" + c.MetricsDatasourceUID + "/api/v1/label/" +
		url.PathEscape(label) + "/values?" + params.Encode()
	resp, err := c.do("GET", endpoint, nil, "")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, readBody(resp.Body))
	}

	var result struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result.Data, nil
}

func readBody(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return strings.TrimSpace(string(b))
}
