# gq

`gq` is a CLI tool and MCP server for querying logs and metrics via Grafana's datasource proxy. It lets developers and AI agents search VictoriaLogs and VictoriaMetrics without direct cluster access or port-forwarding.

## Install

```sh
# via mise (pre-built binary for your platform)
mise use --global github:Staffbase/gq@latest

# or with Go (builds from source)
go install github.com/Staffbase/gq/cmd/gq@latest
```

You can also grab a pre-built tarball from the [releases page](https://github.com/Staffbase/gq/releases) and put `gq` on your `PATH`.

## Configuration

`gq` reads connection settings from a JSON config file. Point to it via the `GRAFANA_CONFIG` environment variable:

```sh
export GRAFANA_CONFIG=~/.config/gq/prod.json
```

The file describes either a single Grafana instance or several. `gq` tells them apart by the `instances` key.

### One instance

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
| `token` | yes, unless `token_command` is set | Grafana service account or API token |
| `token_command` | no | Shell command that prints a fresh token — see [Refreshing expired tokens](#refreshing-expired-tokens) |
| `logs_datasource_uid` | yes | UID of the VictoriaLogs datasource — find it in Grafana under Administration → Data Sources |
| `metrics_datasource_uid` | yes | UID of the VictoriaMetrics datasource — find it in Grafana under Administration → Data Sources |

### Several instances

Give the file an `instances` map and one `gq` process — and one MCP server — serves them all. Each key is an instance name you pass to `--instance` on the CLI, or as the `instance` argument to every MCP tool.

```json
{
  "token_command": "your-auth-helper --print-token --for {url}",
  "instances": {
    "prod": {
      "url": "https://grafana.example.com",
      "logs_datasource_uid": "prod-logs",
      "metrics_datasource_uid": "prod-metrics"
    },
    "staging": {
      "url": "https://grafana.staging.example.com",
      "token": "glsa_...",
      "logs_datasource_uid": "staging-logs",
      "metrics_datasource_uid": "staging-metrics"
    }
  }
}
```

Each instance takes the same fields as the single-instance form. A top-level `token_command` applies to every instance that does not set its own, with `{url}` replaced by that instance's URL — so one helper can mint tokens for all of them. `staging` above pins a static token instead and never runs the command.

`gq` ships no instance list of its own: the names, URLs and datasource UIDs are entirely yours.

### Refreshing expired tokens

A long-running MCP server outlives most tokens. Rather than fail every call after the first expiry, set `token_command` to something that prints a fresh token on stdout:

```json
{ "token_command": "vault read -field=token secret/grafana" }
```

On a `401`, `gq` runs the command via `sh -c`, takes its trimmed stdout as the new token, and retries the request once. If the retry also fails, the error surfaces normally — the command runs once per failure, never in a loop. A non-zero exit or empty output is reported with the command's stderr, so a broken auth helper says so rather than looking like a Grafana outage.

Concurrent calls that all hit an expired token refresh once between them, not once each.

`token_command` is a config-file field. It does not apply to cookie auth, where the cookie is what was rejected and a new token could not help.

### Environment variables instead of a file

```sh
export GRAFANA_URL=https://your-grafana-instance.example.com
export GRAFANA_SERVICE_ACCOUNT_TOKEN=glsa_...   # or GRAFANA_COOKIE=grafana_session=...
export GRAFANA_LOGS_DATASOURCE_UID=<your-logs-datasource-uid>
export GRAFANA_METRICS_DATASOURCE_UID=<your-metrics-datasource-uid>
```

Use `GRAFANA_COOKIE` instead of `GRAFANA_SERVICE_ACCOUNT_TOKEN` if you prefer session-cookie auth (e.g. from a browser session). When both are set, `GRAFANA_COOKIE` takes precedence.

`GRAFANA_CONFIG` wins over all of these. This path covers one instance and has no `token_command` equivalent; use a config file if you need either.

## CLI Usage

```sh
# Query logs (LogsQL)
gq query -q "severity:ERROR _time:1h"
gq query -q "k8s.namespace.name:my-service _time:15m" --limit 50

# Range metrics query (PromQL)
gq metrics -q "up{namespace=\"my-service\"}" --start now-1h --step 60s

# Instant metrics query (PromQL)
gq instant -q "http_requests_total{namespace=\"my-service\"}"

# Pick an instance, when GRAFANA_CONFIG holds several
gq query -q "severity:ERROR _time:1h" --instance prod
gq metrics -q "up" --instance staging

# Print version, commit, and build date
gq version
```

`--instance` is required when the config file defines several, and rejected when it does not. Naming one that does not exist lists the ones that do.

## MCP Server

`gq` can run as an [MCP](https://modelcontextprotocol.io) server over stdio, exposing four tools to AI agents:

| Tool | Description |
|---|---|
| `query_logs` | Run a LogsQL query against VictoriaLogs |
| `query_metrics` | Run a PromQL range query against VictoriaMetrics |
| `query_metrics_instant` | Run a PromQL instant query against VictoriaMetrics |
| `list_label_values` | List distinct values for a metric label |

When `GRAFANA_CONFIG` points at a [multi-instance file](#several-instances), each tool takes one extra required argument, `instance`, and its description lists the names available. One server entry then covers every environment — worth doing, because each entry's tools occupy space in the agent's context whether or not they get called.

### Claude Code

```sh
claude mcp add gq --env GRAFANA_CONFIG=/Users/you/.config/gq/grafana.json -- gq mcp
```

### OpenCode configuration

```json
{
  "mcp": {
    "gq": {
      "type": "local",
      "command": ["gq", "mcp"],
      "environment": {
        "GRAFANA_CONFIG": "/Users/you/.config/gq/grafana.json"
      }
    }
  }
}
```

### Claude Desktop configuration

```json
{
  "mcpServers": {
    "gq": {
      "command": "gq",
      "args": ["mcp"],
      "env": {
        "GRAFANA_CONFIG": "/Users/you/.config/gq/grafana.json"
      }
    }
  }
}
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). All contributors must sign the [CLA](CLA.md).

## License

Apache 2.0 — see [LICENSE](LICENSE).
