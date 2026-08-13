# WeeWoo

![Flashing alarm](docs/Emergency_Light.gif)
<!--
The Emergency Light is licensed under Creative Commons Attribution-Share Alike 3.0 Unported.
See NOTICE.md for details.
-->

## Database

Each WeeWoo server uses either PostgreSQL or its own SQLite database. Select the
backend explicitly in `config.yaml`:

```yaml
database: postgresql
connection_string: postgresql://weewoo:weewoo@localhost/weewoo
```

For a single-server SQLite installation, provide the database file path:

```yaml
database: sqlite
connection_string: /var/lib/weewoo/weewoo.db
```

The public container reserves `/var/lib/weewoo` for persistent data. Mount a
Docker volume there when using SQLite.

## Running the public container

The container reads `config.yaml` from its `/app` working directory by default:

```shell
docker run --rm \
  -p 8080:8080 \
  -p 5000:5000 \
  -v ./config.yaml:/app/config.yaml:ro \
  -v weewoo-data:/var/lib/weewoo \
  ghcr.io/uncertaintea-io/weewoo:latest
```

Set `WEEWOO_CONFIG` to use another path, or pass `-config` explicitly. The
command-line flag takes precedence over the environment variable.

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

## License

The application source code is source-available under the
PolyForm Internal Use License 1.0.0.

You may use and modify the software for internal purposes, including
within a business. The license does not permit offering the software
to third parties as a product or service.

Some components are licensed separately:

- A proprietary runtime binary is distributed under separate terms.
- Company logos and trademarks are not licensed for reuse.
- Third-party dependencies remain subject to their respective licenses.

See [NOTICE.md](NOTICE.md) for details.
