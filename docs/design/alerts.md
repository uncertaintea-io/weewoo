# Alerts and anomaly review

## Purpose

The Alerts page tells users when their service behaved anomalously or when
WeeWoo could not monitor it reliably. PostgreSQL is the durable source of
truth. Alertmanager routes the same alerts to external receivers.

## Model

- An **Alert** is a condition with a firing or resolved lifecycle.
- An **Occurrence** is immutable evidence attached to an Alert.
- A **Notification** is a firing, severity update, refresh, or resolution
  handed to Alertmanager.
- A **Review** optionally accepts one Bad chunk as normal. It never erases the
  automated Verdict.

An anomaly condition is keyed by service and indicator. Its first Bad chunk
opens a warning. Three consecutive Bad chunks escalate the same condition to
critical. A Good chunk resolves it. Every Bad chunk remains a separate
Occurrence so it can be reviewed independently. If all Occurrences are
accepted as normal, the condition resolves with that reason.

Collection failures use one condition per service. Every attempt is an
Occurrence. The third consecutive failure is critical, and the first
successful collection resolves the condition. Pausing or deleting a service
closes active conditions without claiming recovery.

Monitoring impairments also group repeated failures by service and operation.
The third occurrence escalates the condition from warning to critical.

## Alert content

Every Alert snapshots:

- a plain-language title and description;
- user impact and a suggested action;
- sanitized technical details;
- service, severity, timestamps, and current state.

Raw upstream errors are never rendered directly without sanitization.
Alertmanager labels contain the stable WeeWoo alert ID; annotations contain the
same title, description, impact, and suggested action as the UI.

## Reviews and ECDF eligibility

Automated Verdict and Review are independent:

| Automated Verdict | Review | ECDF eligible |
| --- | --- | --- |
| Good | none | yes |
| Bad | none | no |
| Bad | accepted as normal | yes |
| Bad | override reverted | no |

Reviews use optimistic concurrency. Repeating the same action is idempotent;
conflicting stale revisions are rejected. Accepted chunks wait for the next
hourly ECDF build. The UI reports eligibility and the latest reference build,
but does not claim exact version membership without a build manifest.

## Baseline and analysis safety

A new service stays in Learning baseline until ten baseline chunks exist.
Baseline chunks are explicitly eligible. After the first reference is
published, new chunks begin Pending and are excluded until analysis produces a
Good Verdict or a Review accepts a Bad Verdict. Live analysis failures remain
excluded and open a Monitoring impaired alert; historical analysis failures
remain excluded without changing alerts.

## Collection recovery

Failed windows are persisted in chronological order. Live collection continues
independently while a background worker recovers the oldest missing window
first and reports monitoring lag. A pending recovery never diverts a newer live
window into the backlog; only a failed attempt does. After one hour of
continuous failure WeeWoo probes hourly rather than stopping permanently.
Backlog entries expire after 24 hours and become Monitoring gaps.

ECDF publication uses the eligible chunks currently available and does not wait
for collection recovery. This lets a new service publish its first reference
and begin live anomaly alerting while historical work remains pending. Later
builds incorporate recovered eligible chunks. A permanent gap breaks anomaly
streaks. Historical analysis records Good or Bad Verdicts for ECDF eligibility,
but never opens, resolves, or sends alerts. Only live time chunks affect alert
conditions and notifications.
Undelivered historical notifications created by an older version are retired
without handoff; resolutions may still be sent for historical alerts that were
already handed off so Alertmanager does not retain stale firing state.

## Delivery and resolution

Alert and outbox writes are transactional. A background dispatcher retries
Alertmanager handoff and refreshes firing conditions. A resolution uses the
same labels as its firing notification. Receivers must enable
`send_resolved: true`.

If firing was never handed off before resolution, it is marked missed rather
than replayed as a stale firing followed by a resolution. WeeWoo reports
handoff to Alertmanager; it does not claim receiver-level delivery.

## Retention

Resolved Alerts and their Occurrences, lifecycle events, and delivery records
are retained for 90 days. A later manual Review restarts that clock. Firing
Alerts are never removed by retention. Review data remains with its time chunk
even after alert presentation history expires.

## Initial global defaults

| Setting | Default |
| --- | --- |
| Alert history retention | 90 days |
| Baseline chunks | 10 |
| Critical consecutive anomalies | 3 |
| Critical consecutive collection failures | 3 |
| Critical consecutive monitoring failures | 3 |
| Recovery probe mode | after 1 hour |
| Recovery probe interval | 1 hour |
| Failed-window backlog lifetime | 24 hours |
