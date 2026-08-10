# WeeWoo

![Wee Woo Wee Woo](minion.gif)

## Database

Each WeeWoo server uses either PostgreSQL or its own SQLite database. Select the
backend explicitly in `config.yaml`:

```yaml
database: postgresql
database_url: postgresql://weewoo:weewoo@localhost/weewoo
```

For a single-server SQLite installation, provide the database file path:

```yaml
database: sqlite
database_url: /var/lib/weewoo/weewoo.db
```

Initialize or update either backend with the same migration command:

```shell
go run ./cmd/migrate -config config.yaml up
```

Each SQLite-backed WeeWoo server should own its database file. Use PostgreSQL
when multiple WeeWoo processes need to share one database.

## Prerequisites

### jecdf

To run the code locally, you will need to download the latest version of the closed-source `jecdf` tool,
available through its [Releases page](https://github.com/uncertaintea-io/db/releases?q=jecdf&expanded=true).
Download the tarball for your operating system and platform, extract the stand-alone `jecdf` binary,
and copy it to the root directory of the respository.

To force a diagnostic build for one service without publishing or changing the
database, run:

```shell
go run ./cmd/jecdf-build -config config.yaml -service-id 1 -indicator-id 1 -output /tmp/jecdf-debug.bin
```

The command uses the service's active generation and the same eligible chunks
as the scheduled publisher. It reports the generation and eligible chunk count;
diagnostics from the `jecdf` process are printed directly to the terminal. Use
`-jecdf /path/to/jecdf` when the binary is not at `./jecdf`.

## Alerts

WeeWoo stores user-visible alert conditions, occurrences, review decisions, and
Alertmanager handoff state in its configured database. The server applies pending database
migrations automatically before it starts workers or accepts requests. To
inspect or apply migrations administratively, run:

```shell
go run ./cmd/migrate -config config.yaml status
go run ./cmd/migrate -config config.yaml up
```

The Alerts page is available at `#alerts`; its JSON endpoint is
`GET /api/alerts`. Accepting an anomalous time chunk as normal changes only that
chunk's eligibility for future hourly ECDF builds. It does not erase the
automated Verdict.

Alertmanager remains the delivery layer. Routes should group on stable fields
such as `service` and `alert_type`, not `weewoo_alert_id`, when nearby anomaly
occurrences should share a notification. Every receiver expected to announce
recoveries must enable resolved notifications:

```yaml
receivers:
  - name: operations
    webhook_configs:
      - url: https://example.invalid/alerts
        send_resolved: true
```

The initial global alert and recovery defaults are inserted by the initial
schema migration. The complete lifecycle and retention
contract is documented in [`docs/design/alerts.md`](docs/design/alerts.md).
