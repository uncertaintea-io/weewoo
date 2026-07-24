# Keep alert history in PostgreSQL and use Alertmanager for delivery

WeeWoo stores Alerts, Occurrences, Reviews, lifecycle history, and a durable
notification outbox in PostgreSQL. Alertmanager receives the same firing and
resolution content shown in the UI, but it is a delivery adapter rather than
the source of history because its alerts expire and deduplicate by labels.

Alert state and its outbox entry commit together. Alertmanager delivery
failures remain delivery metadata to avoid recursive alerts. PostgreSQL
unavailability is the one emergency exception: WeeWoo may notify Alertmanager
directly and reconciles durable history after storage recovers.
