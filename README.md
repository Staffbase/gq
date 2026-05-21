# gq

`gq` is a CLI tool and MCP server for querying logs and metrics via Grafana's datasource proxy. It lets developers and AI agents search VictoriaLogs and VictoriaMetrics without direct cluster access or port-forwarding.

## Install

```sh
brew install staffbase/tap/gq
```

Or with Go:

```sh
go install github.com/Staffbase/gq/cmd/gq@latest
```

## Configuration

`gq` reads connection settings from a JSON config file. Point to it via the `GRAFANA_CONFIG` environment variable:

```sh
export GRAFANA_CONFIG=~/.config/gq/prod.json
```

Config file format (`~/.config/gq/prod.json`):

```json
{
  "url": "https://your-grafana-instance.example.com",
  "token": "glsa_...",
  "logs_datasource_uid": "<find in Grafana under Administration → Data Sources>",
  "metrics_datasource_uid": "<find in Grafana under Administration → Data Sources>"
}
```

| Field | Required | Description |
|---|---|---|
| `url` | yes | Grafana base URL |
| `token` | yes | Grafana service account or API token |
| `logs_datasource_uid` | yes | UID of the VictoriaLogs datasource — find it in Grafana under Administration → Data Sources |
| `metrics_datasource_uid` | yes | UID of the VictoriaMetrics datasource — find it in Grafana under Administration → Data Sources |

Alternatively, set environment variables directly:

```sh
export GRAFANA_URL=https://your-grafana-instance.example.com
export GRAFANA_SERVICE_ACCOUNT_TOKEN=glsa_...   # or GRAFANA_COOKIE=grafana_session=...
export GRAFANA_LOGS_DATASOURCE_UID=<your-logs-datasource-uid>
export GRAFANA_METRICS_DATASOURCE_UID=<your-metrics-datasource-uid>
```

Use `GRAFANA_COOKIE` instead of `GRAFANA_SERVICE_ACCOUNT_TOKEN` if you prefer session-cookie auth (e.g. from a browser session). When both are set, `GRAFANA_COOKIE` takes precedence.

## CLI Usage

```sh
# Query logs (LogsQL)
gq query -q "severity:ERROR _time:1h"
gq query -q "k8s.namespace.name:my-service _time:15m" --limit 50

# Range metrics query (PromQL)
gq metrics -q "up{namespace=\"my-service\"}" --start now-1h --step 60s

# Instant metrics query (PromQL)
gq instant -q "http_requests_total{namespace=\"my-service\"}"

# Print version, commit, and build date
gq version
```

## MCP Server

`gq` can run as an [MCP](https://modelcontextprotocol.io) server over stdio, exposing four tools to AI agents:

| Tool | Description |
|---|---|
| `query_logs` | Run a LogsQL query against VictoriaLogs |
| `query_metrics` | Run a PromQL range query against VictoriaMetrics |
| `query_metrics_instant` | Run a PromQL instant query against VictoriaMetrics |
| `list_label_values` | List distinct values for a metric label |

### OpenCode configuration

Add one entry per Grafana environment to your `opencode.json`:

```json
{
  "mcp": {
    "gq-prod": {
      "type": "local",
      "command": ["gq", "mcp"],
      "environment": {
        "GRAFANA_CONFIG": "/Users/you/.config/gq/prod.json"
      }
    }
  }
}
```

### Claude Desktop configuration

```json
{
  "mcpServers": {
    "gq-prod": {
      "command": "gq",
      "args": ["mcp"],
      "env": {
        "GRAFANA_CONFIG": "/Users/you/.config/gq/prod.json"
      }
    }
  }
}
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). All contributors must sign the [CLA](CLA.md).

## License

Apache 2.0 — see [LICENSE](LICENSE).
