# WeeWoo

WeeWoo detects anomalous service behavior and maintains the reference distributions used to evaluate newly collected observations.

## Language

**Time chunk**:
A service's collected load and latency observations for one sampling interval.
_Avoid_: Sample

**Verdict**:
The mutable assessment that determines whether a time chunk is eligible for joint ECDF builds.

**Good chunk**:
A time chunk with no negative verdict, or with a positive verdict. Good chunks are eligible for joint ECDF builds.
_Avoid_: Pending chunk

**Bad chunk**:
A time chunk whose anomaly test crossed the alerting threshold. Its negative verdict excludes it from future joint ECDF builds.
_Avoid_: Deleted chunk
