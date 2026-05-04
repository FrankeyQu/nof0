# Production Preflight

`cmd/preflight` is the release gate for production-oriented deployments. It
loads the same application config as the API process and fails before startup
when required trading, persistence, LLM, exchange, or hard-risk settings are
missing.

The main API entrypoint (`nof0.go`) also executes the same gate when
`Env=prod`.

The Docker image entrypoint `start.sh` runs the same gate before `nof0-api`
when the selected config or environment indicates production.

## Run

```bash
cd go
make preflight CONFIG_FILE=etc/nof0.prod.yaml
```

Direct command:

```bash
go run cmd/preflight/main.go -f etc/nof0.prod.yaml -profile trading
```

Profiles:

| Profile | Purpose |
| --- | --- |
| `trading` | Production trading process. Requires `Env=prod`, Postgres, Redis cache, LLM, exchange/market providers, hard risk settings, and `CommandWorker.Enabled=true`. |
| `api` | Production API process. Requires production persistence and runtime config, but does not require the command worker. |

JSON output is available for CI:

```bash
go run cmd/preflight/main.go -f etc/nof0.prod.yaml -profile trading -format json
```

The CI workflow also runs a matching smoke job against the checked-in prod
template with dummy environment values.

## What Blocks Startup

Fatal findings return exit code `1`. The current hard gates include:

- `Env` must be `prod`.
- `Postgres.DataSource` and at least one Redis cache node must be configured.
- Trading profile requires `CommandWorker.Enabled=true` so queued approvals can be executed by the API process.
- LLM config must have a non-placeholder API key, a known default model, and strict budget enforcement.
- Executor prompt and output validation must be strict.
- Manager, exchange, and market configs must be present and cross-referenced correctly.
- Every trader must define a model, allocation, execution mode target, symbol whitelist, daily loss limit, margin cap, stop loss, and take profit.
- Live trading requires:
  - `ASTRAQUANT_ALLOW_LIVE_TRADING=true`
  - `ASTRAQUANT_LIVE_TRADING_ACK=I_UNDERSTAND_THIS_CAN_LOSE_MONEY`
- `Cors.AllowOrigins: ['*']` is blocked when the main config is `Env=prod`.

Warnings do not fail the command but should be reviewed before real capital is
enabled. Examples include `sslmode=disable`, verbose SQL logging, missing
liquidity guard thresholds, and missing candidate limits.

## Recommended Release Order

1. Create a dedicated production config such as `etc/nof0.prod.yaml`.
2. Set secrets through environment variables or a secret store; do not commit them.
3. Run database migrations.
4. Run `make preflight CONFIG_FILE=etc/nof0.prod.yaml`.
5. Start the API and probe `/healthz` and `/readyz`.
6. Start the trading manager process only after the preflight is clean.
