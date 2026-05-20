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
	"errors"
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
//  1. GRAFANA_CONFIG env var → load config file (all settings in the file)
//  2. Individual env vars: GRAFANA_URL, GRAFANA_SERVICE_ACCOUNT_TOKEN or
//     GRAFANA_COOKIE, GRAFANA_LOGS_DATASOURCE_UID, GRAFANA_METRICS_DATASOURCE_UID
//
// All fields are required. Find datasource UIDs in Grafana under
// Administration → Data Sources → <datasource> → UID.
//
// Returns an error if any required field is missing.
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
	logsUID := os.Getenv("GRAFANA_LOGS_DATASOURCE_UID")
	if logsUID == "" {
		return nil, fmt.Errorf("GRAFANA_LOGS_DATASOURCE_UID environment variable is required (find it in Grafana under Administration → Data Sources)")
	}
	metricsUID := os.Getenv("GRAFANA_METRICS_DATASOURCE_UID")
	if metricsUID == "" {
		return nil, fmt.Errorf("GRAFANA_METRICS_DATASOURCE_UID environment variable is required (find it in Grafana under Administration → Data Sources)")
	}
	return &Client{
		BaseURL:              strings.TrimRight(baseURL, "/"),
		Cookie:               cookie,
		Token:                token,
		LogsDatasourceUID:    logsUID,
		MetricsDatasourceUID: metricsUID,
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
	startTS, err := resolveTime(start)
	if err != nil {
		return nil, err
	}
	endTS, err := resolveTime(end)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("query", query)
	if startTS != "" {
		form.Set("start", startTS)
	}
	if endTS != "" {
		form.Set("end", endTS)
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

	// Read NDJSON response line by line, skipping blank lines.
	var lines []string
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			lines = append(lines, line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("reading response: %w", err)
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// QueryMetricsRange runs a PromQL range query against VictoriaMetrics via the Grafana proxy.
// Returns the raw JSON response body.
func (c *Client) QueryMetricsRange(query, start, end, step string) ([]byte, error) {
	startTS, err := resolveTime(start)
	if err != nil {
		return nil, err
	}
	endTS, err := resolveTime(end)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("query", query)
	if startTS != "" {
		params.Set("start", startTS)
	}
	if endTS != "" {
		params.Set("end", endTS)
	}
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
	tTS, err := resolveTime(t)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("query", query)
	if tTS != "" {
		params.Set("time", tTS)
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
		Error  string   `json:"error"`
		Data   []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if result.Status != "success" {
		if result.Error != "" {
			return nil, fmt.Errorf("datasource error: %s", result.Error)
		}
		return nil, fmt.Errorf("unexpected status %q in response", result.Status)
	}
	return result.Data, nil
}

func readBody(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return strings.TrimSpace(string(b))
}
