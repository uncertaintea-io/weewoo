# WeeWoo

WeeWoo detects anomalous service behavior and maintains the reference distributions used to evaluate newly collected observations.

## Language

**Time chunk**:
A service's collected load and latency observations for one sampling interval.
_Avoid_: Sample

**Time-of-day chunk**:
An observation used by the Load vs. UTC Time of Day indicator. Its X
value is the service-interval bucket since UTC midnight and its Y value is load.
Its durable timestamp is the original UTC Prometheus observation time.

**Load vs. UTC Time of Day**:
The indicator that compares recent load with load historically observed in the
same service-interval buckets of a UTC day. It is indicator ID 2.

**Verdict**:
The automated assessment of a time chunk. A later Review override may change
the chunk's eligibility without erasing this assessment.

**Good chunk**:
A time chunk with a positive Verdict. Good chunks are eligible for joint ECDF
builds.

**Bad chunk**:
A time chunk whose anomaly test crossed the alerting threshold. Its negative verdict excludes it from future joint ECDF builds.
_Avoid_: Deleted chunk

**Baseline chunk**:
A time chunk intentionally admitted while a service is learning its first
reference distribution.

**Pending chunk**:
A post-baseline time chunk awaiting a Verdict. Pending chunks are not eligible
for joint ECDF builds.

**Review**:
An optional human decision that accepts a Bad chunk as normal or restores its
automated Verdict. A Review applies only to the selected time chunk.
_Avoid_: Verdict correction

**Alert**:
A user-visible condition that may contain one or more Occurrences and has a
firing or resolved lifecycle.

**Occurrence**:
Immutable evidence that contributed to an Alert, such as one Bad chunk or one
failed collection attempt.

**Notification**:
An Alertmanager handoff for an Alert firing, changing severity, or resolving.
_Avoid_: Alert

**Monitoring gap**:
A collection window for which WeeWoo could not recover metrics. A Monitoring
gap is neither a Good chunk nor a Bad chunk.
