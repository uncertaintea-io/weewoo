# Interval Scheduler Realignment Spec

## Problem

The collection scheduler currently schedules windows on an interval alignment boundary derived from `time.Time.Truncate(interval)`. When a service changes interval, the next window can be recalculated from the new interval without preserving the previous successful collection endpoint. That can create broken scheduler state, missing collection gaps, or overlapping samples that later have to be differentiated before writing ECDF data.

The scheduler needs explicit per-callback state so interval changes can preserve continuous collection history while moving future collection onto the new interval boundary.

## Goals

- Preserve contiguous collection windows for each callback ID.
- Avoid overlapping successful collection windows for the same callback ID.
- Ensure no collection window is larger than `max(oldInterval, newInterval)` during an interval transition.
- Move steady-state collection onto the new interval boundary after an interval update.
- Retry failed windows without blocking unrelated callbacks.
- Distinguish retryable failures from permanent failures.
- Make scheduler lifecycle, observability, and recovery behavior explicit.
- Keep interval boundary semantics compatible with the current `time.Time.Truncate(interval)` behavior.

## Non-Goals

- Persist scheduler state directly inside the scheduler.
- Teach the scheduler about ECDF storage or Prometheus.
- Guarantee durable gap-free collection after failed writes unless the callback reports success or failure accurately.
- Introduce a new human-clock alignment model for non-round intervals.

## Definitions

- **Callback ID**: Stable identity for one scheduled collection stream, such as a service ID.
- **Window**: A half-open collection range represented by `[start, end]` in the current code API.
- **`lastEnd`**: The end timestamp of the last successfully collected window for a callback ID.
- **Aligned boundary**: A timestamp produced by `time.Time.Truncate(interval)` math. For a given `interval`, the next boundary after `t` is `t.Truncate(interval)` if equal to `t`, otherwise `t.Truncate(interval).Add(interval)`.
- **Transition window**: A window emitted after an interval update before the callback reaches the new interval alignment boundary.
- **Steady-state window**: A normal window of exactly the active interval, ending on the active interval alignment boundary.

## Public API Proposal

The scheduler should use callback IDs and explicit callback results.

```go
type IntervalFunction func(ctx context.Context, start, end time.Time) IntervalResult

type IntervalResult struct {
	Err       error
	Retryable bool
}

func IntervalSuccess() IntervalResult
func IntervalRetry(err error) IntervalResult
func IntervalPermanent(err error) IntervalResult
```

`IntervalResult` semantics:

- Success advances `lastEnd` to `end`.
- Retryable failure retries the same exact window with backoff and does not advance `lastEnd`.
- Permanent failure disables the callback and does not advance `lastEnd`.

The scheduler should expose an upsert API:

```go
func (s *IntervalScheduler) AddCallback(id int, interval time.Duration, fn IntervalFunction, opts ...CallbackOption) error
func (s *IntervalScheduler) RemoveCallback(id int)
func (s *IntervalScheduler) Stop()
```

`AddCallback` behavior:

- If `id` is new, create a callback.
- If `id` exists, update its interval and function.
- If the callback is currently in flight, record the update as pending and apply it after the in-flight invocation returns.
- If the callback is disabled by permanent failure, an update clears the disabled state and resumes from the unchanged `lastEnd`.
- Updates take effect from `lastEnd`, not from the wall-clock time when the update was requested.

`CallbackOption` should support an optional `lastEnd`:

```go
func WithLastEnd(t time.Time) CallbackOption
```

If `lastEnd` is absent for a new callback, use the current cold-start behavior: first collection is `[nextBoundary - interval, nextBoundary]`.

## Errors

Define sentinel errors checkable with `errors.Is`:

- `ErrInvalidInterval`
- `ErrSchedulerStopped`
- `ErrNilCallback`

`AddCallback` rejects `interval <= 0`, nil callbacks, and calls after scheduler stop begins.

## Interval Update Algorithm

When a callback changes interval:

1. Preserve the callback's current `lastEnd`.
2. Let `oldInterval` be the interval active for the last successful window.
3. Let `newInterval` be the requested interval.
4. Let `cap = max(oldInterval, newInterval)`.
5. Find the next aligned boundary for `newInterval` at or after `lastEnd`.
6. Emit transition windows from `lastEnd` toward that boundary without gaps or overlaps.
7. No transition window may be larger than `cap`.
8. The final transition window must end exactly on the new interval boundary.
9. After reaching the new boundary, emit steady-state windows of exactly `newInterval`.

Example:

- Old interval: `1m`
- New interval: `2m`
- `lastEnd`: `12:01`
- Next new boundary: `12:02`
- Cap: `2m`

Transition windows:

- `[12:01, 12:02]`

Then steady-state:

- `[12:02, 12:04]`
- `[12:04, 12:06]`

Transition windows are paced by their end time. The scheduler must not dispatch a window whose `end` is in the future. If a transition or catch-up window's `end <= now`, it is due immediately.

## Retry Behavior

Retries are isolated per callback ID.

On retryable failure:

- Retry the same exact `[start, end]` window.
- Do not advance `lastEnd`.
- Do not start later windows for that same callback ID until the failed window succeeds.
- Reinsert the callback with a per-callback retry `nextAttemptAt`.
- Other callback IDs continue to run normally.

Backoff should be configurable through scheduler options.

Default backoff:

- Initial delay: `1s`
- Multiplier: `2`
- Maximum delay: `min(activeInterval, 1m)`
- Jitter: disabled by default for deterministic behavior unless explicitly configured

Backoff resets after:

- A successful window.
- An update that clears a permanent failure.

## Permanent Failure Behavior

On permanent failure:

- Disable that callback ID.
- Do not advance `lastEnd`.
- Do not retry automatically.
- Do not skip the failed window silently.
- Other callback IDs continue normally.

The callback resumes only when it is updated through `AddCallback` with the same ID. The update clears the disabled state, keeps the unchanged `lastEnd`, applies the new interval/function, and recomputes transition windows from that point.

## Concurrency

Execution is serialized per callback ID and independent across callback IDs.

Rules:

- A callback ID may have at most one in-flight invocation.
- A callback ID processes windows in order.
- It must not start `[12:01, 12:02]` before `[12:00, 12:01]` succeeds.
- Different callback IDs may execute concurrently.
- A slow, retrying, or permanently failed callback must not block unrelated callbacks.

This requires changing the current synchronous scheduler loop. The scheduler loop should dispatch due callbacks and receive completion results asynchronously so it can continue handling other due callbacks, updates, removals, and stop requests.

## In-Flight Updates

If `AddCallback` is called for a callback ID that is currently executing:

- Store the update as pending.
- Let the in-flight invocation finish with the old interval/function.
- If it succeeds, advance `lastEnd` to the completed window's `end`.
- Apply the pending update after completion.
- Recompute transition windows from the updated `lastEnd`.

If the in-flight invocation fails retryably, the pending update should still be applied after the invocation returns, because the operator explicitly changed the callback configuration. The failed window remains anchored at unchanged `lastEnd`, and transition planning starts from that point with the new interval/function.

If multiple updates arrive while one invocation is in flight, the latest update wins.

## Catch-Up Behavior

If a callback finishes after one or more of its future windows are already due:

- Advance `lastEnd` after success.
- Compute the next window from `lastEnd`.
- If the next window's `end <= now`, schedule it immediately.
- Continue catching up sequentially for that callback ID.
- Never run more than one invocation concurrently for the same callback ID.

## Lifecycle

`RemoveCallback(id)`:

- Removes a queued callback.
- If the callback is in flight, cancels its context and marks removal pending.
- Does not reinsert the callback after the in-flight invocation returns.
- Removing a missing ID is a no-op.
- Removal drops scheduler memory state for that ID, including `lastEnd`, retry state, disabled state, and pending updates.

`Stop()`:

- Is graceful and blocking.
- Is safe to call more than once.
- Marks the scheduler stopped.
- Prevents new dispatch.
- Cancels all in-flight callback contexts.
- Waits for all in-flight callbacks to return.
- Causes later `AddCallback` calls to return `ErrSchedulerStopped`.

If a callback returns because its context was canceled during `Stop()` or `RemoveCallback`, the scheduler should not treat that as a retryable collection failure.

## Observability

The scheduler should emit structured events and provide default logging through `slog.Default()`.

Callers can override the event handler or disable it with a no-op handler.

Events should include:

- Callback added.
- Callback updated.
- Callback removed.
- Window succeeded.
- Window retry scheduled.
- Callback disabled by permanent failure.
- Callback resumed by update.
- Scheduler stopped.

Window-related events should include:

- Callback ID.
- Window start.
- Window end.
- Active interval.
- Attempt count.
- Error, when present.

The scheduler should emit structured event data. The default event handler maps that data to `slog` fields.

## Restart and Recovery

Scheduler state is in-process only.

For restart recovery:

- The caller may read the last persisted successful collection timestamp from storage.
- The caller passes that timestamp as `WithLastEnd(t)` when adding the callback.
- The scheduler then computes transition or steady-state windows from that timestamp using the same no-gap/no-overlap/max-window rules.

If no `lastEnd` is provided, use cold-start behavior:

- Compute the next active interval boundary from `now`.
- First window is `[nextBoundary - interval, nextBoundary]`.

## Acceptance Tests

Scheduler tests should use an injectable clock/timer abstraction rather than real sleeps.

Required tests:

1. New callback without `lastEnd` preserves current cold-start behavior.
2. New callback with `lastEnd` starts from that timestamp.
3. Interval update emits no gaps and no overlaps.
4. Transition windows are split so no window exceeds `max(oldInterval, newInterval)`.
5. Final transition window lands exactly on the new interval boundary.
6. Steady-state windows after transition use the new interval.
7. Transition windows are not dispatched before their end time.
8. Catch-up windows whose end time is in the past are dispatched immediately and sequentially.
9. Retryable failure retries the same exact window.
10. Retryable failure does not advance `lastEnd`.
11. Retry backoff increases and caps according to policy.
12. Retry state is per callback ID.
13. A retrying callback does not block another callback ID.
14. Permanent failure disables only that callback ID.
15. Permanent failure does not advance `lastEnd`.
16. Updating a permanently failed callback clears disabled state and resumes from unchanged `lastEnd`.
17. Updates during in-flight execution are applied after completion.
18. Multiple in-flight updates collapse to the latest update.
19. Same callback ID never has concurrent invocations.
20. Different callback IDs may run concurrently.
21. Long successful callback catches up sequentially.
22. `RemoveCallback` removes queued callback state.
23. `RemoveCallback` cancels in-flight callback state and prevents reinsertion.
24. `Stop` cancels and waits for in-flight callbacks.
25. `AddCallback` after `Stop` returns `ErrSchedulerStopped`.
26. Invalid intervals and nil callbacks return sentinel errors.
27. Default event handler logs through `slog`.
28. Custom event handler receives structured scheduler events.

## Implementation Notes

- Keep the scheduler independent from Prometheus and ECDF storage.
- Store callback state in a map keyed by callback ID, plus a priority queue or sorted structure ordered by next due time or retry attempt time.
- Track per-callback fields: ID, interval, function, `lastEnd`, next due window, in-flight state, pending update, retry attempt count, disabled state, cancellation function, and removal flag.
- Avoid sleeping inside callback execution paths. Dispatch work in separate goroutines and send completion results back to the scheduler loop.
- Use an injectable clock for `Now`, timers, and manual test advancement.
- Keep `NewIntervalScheduler()` as the simple production constructor with real clock, default backoff, and default `slog` event handler.
- Add an options constructor or functional options for fake clock, backoff policy, and event handler.

## Migration Steps

1. Introduce `IntervalResult`, result helpers, sentinel errors, scheduler options, and event types.
2. Add fake-clock-capable scheduler internals and tests for current behavior.
3. Replace synchronous callback execution with per-callback asynchronous dispatch and completion handling.
4. Add per-callback `lastEnd` tracking and cold-start behavior.
5. Implement retryable and permanent failure states.
6. Implement interval update transition planning.
7. Implement remove and stop lifecycle behavior.
8. Update `collector.Schedule` to return `IntervalResult` from collection success/failure.
9. Add observability defaults with `slog`.
10. Remove or update old sleep-based tests once deterministic tests cover the behavior.
