# Changelog

## [0.0.1](https://github.com/Staffbase/gq/releases/tag/v0.0.1) (2026-05-21)

First release. `gq` queries logs and metrics through Grafana's datasource proxy,
so you can search VictoriaLogs and VictoriaMetrics without cluster access or a
port-forward.

### Features

- **CLI** — `gq query` runs LogsQL against VictoriaLogs, `gq metrics` runs a
  PromQL range query, `gq instant` runs a PromQL instant query, and `gq version`
  prints the version, commit and build date. Time arguments accept RFC3339, Unix
  timestamps, or relative forms like `1h` and `now-30m`.
- **MCP server** — `gq mcp` speaks the Model Context Protocol over stdio and
  exposes four tools to AI agents: `query_logs`, `query_metrics`,
  `query_metrics_instant` and `list_label_values`.
- **Configuration** — settings come from a JSON file pointed at by
  `GRAFANA_CONFIG`, or from `GRAFANA_*` environment variables. Authentication is
  a service account token or a session cookie; the cookie wins when both are set.
- **Releases** — GoReleaser publishes static binaries for Linux and macOS on
  amd64 and arm64, installable via `mise` or straight from the releases page.
