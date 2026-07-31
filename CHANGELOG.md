# Changelog

## [0.2.0](https://github.com/Staffbase/gq/compare/v0.1.0...v0.2.0) (2026-07-31)


### Features

* **release:** bump the Homebrew formula on release ([#40](https://github.com/Staffbase/gq/issues/40)) ([f58a335](https://github.com/Staffbase/gq/commit/f58a33522ce747ad1e2523956bd2b16b2ee8ca72))


### Bug Fixes

* **release:** refuse to bump the tap on an unusable version ([#41](https://github.com/Staffbase/gq/issues/41)) ([fff971d](https://github.com/Staffbase/gq/commit/fff971df796e4fd487bed2a11efa20ae06ea009e))
* **release:** stop the CLA lock from killing releases, add a recovery path ([#36](https://github.com/Staffbase/gq/issues/36)) ([3a7ef1a](https://github.com/Staffbase/gq/commit/3a7ef1a5444f17efcef8a123af96cba0b327d3ad))

## [0.1.0](https://github.com/Staffbase/gq/compare/v0.0.1...v0.1.0) (2026-07-31)


### Features

* **multi-instance config.** A config file with an `instances` map describes several Grafana instances at once, each with its own URL, credentials and datasource UIDs. Pick one with `--instance <name>`. The format is auto-detected, so existing single-instance configs and `GRAFANA_*` environment variables keep working unchanged ([#23](https://github.com/Staffbase/gq/pull/23)) ([a142a18](https://github.com/Staffbase/gq/commit/a142a180bf4565154c203d4b8e76005d85f04330))
* **automatic token refresh.** Set `token_command` to any shell command that prints a token. On a 401 `gq` runs it, replaces the token and retries the request once, so a session that outlives its token no longer fails. `{url}` in the command is replaced with the instance URL, so one command at the top of the config can serve every instance ([#23](https://github.com/Staffbase/gq/pull/23)) ([a142a18](https://github.com/Staffbase/gq/commit/a142a180bf4565154c203d4b8e76005d85f04330))
* **one MCP server for all instances.** In multi-instance mode `gq mcp` takes `instance` as a tool argument instead of needing a server process per environment — four tools in an agent's context rather than four per instance. Connections and tokens are reused per instance across calls ([#23](https://github.com/Staffbase/gq/pull/23)) ([a142a18](https://github.com/Staffbase/gq/commit/a142a180bf4565154c203d4b8e76005d85f04330))


### Bug Fixes

* a `GRAFANA_CONFIG` that is set but unreadable or malformed now reports the file and the parse error, instead of silently falling back to environment variables and complaining that `GRAFANA_URL` is missing ([#23](https://github.com/Staffbase/gq/pull/23)) ([a142a18](https://github.com/Staffbase/gq/commit/a142a180bf4565154c203d4b8e76005d85f04330))
* `gq mcp` reports the real binary version in its MCP handshake instead of a hard-coded one ([#23](https://github.com/Staffbase/gq/pull/23)) ([a142a18](https://github.com/Staffbase/gq/commit/a142a180bf4565154c203d4b8e76005d85f04330))

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
