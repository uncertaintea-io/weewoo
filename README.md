# WeeWoo

![Wee Woo Wee Woo](minion.gif)

## Prerequisites

### jecdf

To run the code locally, you will need to download the latest version of the closed-source `jecdf` tool,
available through its [Releases page](https://github.com/uncertaintea-io/db/releases?q=jecdf&expanded=true).
Download the tarball for your operating system and platform, extract the stand-alone `jecdf` binary,
and copy it to the root directory of the respository.

## Alerts

WeeWoo stores user-visible alert conditions, occurrences, review decisions, and
Alertmanager handoff state in PostgreSQL. Apply migrations before starting a
server built from a newer revision:

```shell
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

The initial global alert and recovery defaults are inserted by migration
`000011_create_alert_history.sql`. The complete lifecycle and retention
contract is documented in [`docs/design/alerts.md`](docs/design/alerts.md).
