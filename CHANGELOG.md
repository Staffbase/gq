# Changelog

## [0.1.0](https://github.com/Staffbase/gq/compare/v0.0.1...v0.1.0) (2026-07-31)

One `gq` process can now serve several Grafana instances, and it keeps itself
authenticated instead of dying when a token expires.

### Features

- **Several instances from one config.** A config file containing an `instances`
  map is read as a registry: each entry has its own URL, datasource UIDs and
  credentials. Pick one with `--instance <name>` on the CLI. The format is
  auto-detected, so an existing single-instance config keeps working untouched.
- **One MCP server instead of one per environment.** In registry mode the MCP
  server takes `instance` as a tool argument, so six environments cost four
  tools in an agent's context rather than twenty-four. Clients are cached per
  instance, so repeated calls reuse one connection and one token.
- **Automatic token refresh.** Set `token_command` to any shell command that
  prints a token. On HTTP 401 `gq` runs it, replaces the token and retries the
  request once. `{url}` in the command is replaced with the instance's URL, so
  one command at the top of a registry can serve every instance. A burst of
  concurrent 401s refreshes once, not once per request. Nothing is retried
  twice, and cookie auth is left alone.

### Bug Fixes

- A `GRAFANA_CONFIG` that is set but unreadable now reports the offending file
  and the parse error. It used to fall through to environment-variable mode and
  complain that `GRAFANA_URL` was missing, which sent people looking in the
  wrong place.
- `{url}` is substituted for every `token_command`, not only for one inherited
  from the registry level. A single-instance config or a per-instance override
  used to pass `{url}` to the shell literally, so the refresh failed.
- The MCP server reports its real version in the handshake instead of a
  hard-coded `0.1.0`.

### Upgrading

Nothing breaks. Existing config files, `GRAFANA_*` environment variables and all
four CLI commands behave exactly as before; everything above is opt-in.

### Chores

Releases are now cut by release-please from conventional commits, `gofmt` is
enforced in CI, and the usual run of dependency and GitHub Actions bumps.

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
