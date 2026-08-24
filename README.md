# WeeWoo

![Flashing alarm](docs/Emergency_Light.gif)
<!--
The Emergency Light is licensed under Creative Commons Attribution-Share Alike 3.0 Unported.
See NOTICE.md for details.
-->

An experimental system for alerting when the performance of a system is out of the ordinary.

This was built as a internship project by @brippy207 in the summer of 2026.
For full details, see the [internship project plan](docs/plans/internship.md).



## Screenshots

<p style="align: top">
  <a href="docs/screenshots/weewoo-service.png">
    <img src="docs/screenshots/weewoo-service.png" alt="Service" width="400" align=top>
  </a>
  <a href="docs/screenshots/weewoo-alert.png">
    <img src="docs/screenshots/weewoo-alert.png" alt="Alert" width="400" align=top>
  </a>
</p>


## Database

Each WeeWoo server uses either a shared PostgreSQL database or a local SQLite database.
The database is selected using a YAML configuration file, typically called `config.yaml`:

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

## Running the server using Docker

By default, the Docker image will use a local SQLite database stored in a volume mounted
at `/var/lib/weewoo/`. If you don't create a volume yourself, Docker will create a temprary
one for you and store the files there. This volume is ephemeral, so you will lose your data
between restarts. To persist the data, create a data directory with:

```shell
mkdir data
chmod 777 data # so it is readable by the nonroot Docker image
```

Then:

```shell
docker run --rm \
  -p 8080:8080 \
  -p 5000:5000 \
  -v ./data:/var/lib/weewoo \
  ghcr.io/uncertaintea-io/weewoo:latest
```

To specify a different configuration, you can override it:

```shell
docker run --rm \
  -p 8080:8080 \
  -p 5000:5000 \
  -v ./config.yaml:/app/config.yaml:ro \
  -v ./data:/var/lib/weewoo \
  ghcr.io/uncertaintea-io/weewoo:latest
```

## Alerts

WeeWoo stores user-visible alert conditions, occurrences, review decisions, and
Alertmanager handoff state in its configured database.

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
