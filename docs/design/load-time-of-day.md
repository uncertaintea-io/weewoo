# Load vs. UTC Time of Day

## Purpose

WeeWoo learns the load normally observed in each part of a UTC day. This is a
second JECDF indicator and does not change the existing Load vs. Latency model.
Its indicator ID is `2`; Load vs. Latency remains indicator ID `1`.

## Collection and chunks

The existing service scheduler remains the only collection scheduler, and its
`interval_seconds` is also the Prometheus range-query step. The load range query
must return one aggregate series. The observations returned for a collection
window are stored together in one Time chunk for indicator 2:

- the chunk has one database and encoded timestamp at whole-second UTC
  precision;
- each load sample contributes an X and Y pair to that chunk;
- X is the sample's whole number of seconds since UTC midnight;
- Y is that sample's observed load;
- the service interval controls sampling cadence but does not scale or otherwise
  transform X.

One chunk may therefore contain several load samples under the same chunk
timestamp. Writes are idempotent. If an indicator-2 write fails, the complete
collection window fails and is retried. Historical imports create the same
chunks and count toward learning, but never emit live historical alerts.

## Learning and publication

A time-of-day position is trained after it contains eligible observations from
five distinct UTC dates. The model is ready when at least 95% of the
`ceil(86,400 / interval_seconds)` expected daily sampling positions are trained.
The interval determines the expected sampling positions for this readiness
calculation; their X coordinates remain seconds since midnight. Counts use
eligible Baseline and Good chunks plus reviewed Bad chunks accepted as normal; raw
observation counts cannot satisfy the distinct-day requirement.

The hourly ECDF publisher defers indicator 2 until it is ready, then publishes
it using the existing immutable database publication path. It uses all eligible
chunks in the current service generation. A material service configuration
change or manual baseline reset advances the shared generation and resets both
indicators. There is no indicator-specific retention or time-window cutoff.

Readiness is continuously enforced at 95%. If it drops below the threshold,
analysis is suspended without resolving or escalating an existing alert. The
last published artifact remains stored and is replaced after readiness returns.

## Analysis and alerting

Analysis covers the trailing five minutes of timestamped load observations,
independent of scheduler interval. Each observation is queried against the
conditional JECDF at its UTC second-of-day and converted to a historical
percentile. A two-sided one-sample KS test compares those percentiles with the
uniform distribution using the existing significance threshold.

The indicator-2 chunk written for a scheduler window receives that window's
single result, which creates at most one alert occurrence. The existing
lifecycle applies: the first Bad result opens a warning, three consecutive Bad
results escalate to critical, and a Good result resolves the condition. Alert
evidence identifies whether load shifted higher or lower. Indicator-2 verdicts,
reviews, eligibility, and alerts remain separate from indicator 1.

## Status and known limits

The service detail API and UI expose the **Load vs. UTC Time of Day** state,
coverage, five-day requirement, and latest publication time.

The MVP pools every UTC date. It does not distinguish weekdays, weekends, or
holidays. That seasonality can be modeled separately later.
