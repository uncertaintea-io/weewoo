package collection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/alerting"
	"github.com/uncertaintea-io/weewoo/internal/config"
)

const (
	defaultBacklogRetention = 24 * time.Hour
	defaultProbeAfter       = time.Hour
	defaultProbeInterval    = time.Hour
)

type historicalCollector func(context.Context, *config.Service, time.Time, time.Time) error

type recoveryTarget struct {
	mu      sync.RWMutex
	service *config.Service
	collect historicalCollector
	wake    chan struct{}
	stop    chan struct{}
	once    sync.Once
}

// RecoveryQueue preserves failed windows and drains them oldest-first without
// blocking the interval scheduler.
type RecoveryQueue struct {
	db       *sql.DB
	cfg      config.Config
	recorder alerting.Recorder
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	targets  map[int]*recoveryTarget
	wg       sync.WaitGroup
}

func NewRecoveryQueue(db *sql.DB, cfg config.Config, recorder alerting.Recorder) *RecoveryQueue {
	ctx, cancel := context.WithCancel(context.Background())
	return &RecoveryQueue{
		db: db, cfg: cfg, recorder: recorder, ctx: ctx, cancel: cancel,
		targets: make(map[int]*recoveryTarget),
	}
}

func (q *RecoveryQueue) Register(service *config.Service, collect historicalCollector) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if target := q.targets[service.Id]; target != nil {
		target.mu.Lock()
		target.service = service
		target.collect = collect
		target.mu.Unlock()
		q.signal(target)
		return
	}
	target := &recoveryTarget{service: service, collect: collect, wake: make(chan struct{}, 1), stop: make(chan struct{})}
	q.targets[service.Id] = target
	q.wg.Add(1)
	go q.run(target)
	q.signal(target)
}

func (q *RecoveryQueue) Unregister(serviceID int) {
	q.mu.Lock()
	target := q.targets[serviceID]
	delete(q.targets, serviceID)
	q.mu.Unlock()
	if target != nil {
		target.once.Do(func() { close(target.stop) })
	}
}

func (q *RecoveryQueue) Stop() {
	q.cancel()
	q.wg.Wait()
}

func (q *RecoveryQueue) ResolveCollection(ctx context.Context, serviceID int, at time.Time) error {
	return q.recorder.ResolveCollection(ctx, serviceID, at)
}

func (q *RecoveryQueue) EnqueueFailure(ctx context.Context, service *config.Service, start, end time.Time, failure error) error {
	retention := configuredRecoveryDuration(q.cfg, "collection_backlog_retention", defaultBacklogRetention)
	retryAt := time.Now().UTC().Add(time.Second)
	if transactional, ok := q.recorder.(alerting.TransactionalCollectionRecorder); ok {
		tx, err := q.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := enqueueFailedWindow(ctx, tx, service, start, end, retryAt, retention, failure); err != nil {
			return err
		}
		logCommittedEvents, err := transactional.RecordCollectionFailureTx(ctx, tx, alerting.CollectionFailure{
			ServiceID: service.Id, ServiceName: service.Name, WindowStart: start, WindowEnd: end,
			Attempt: 1, RetryAt: retryAt, Error: failure,
		})
		if err != nil {
			return fmt.Errorf("record collection failure: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		logCommittedEvents()
		q.wake(service.Id)
		return nil
	}
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO collection_backlog (
			service_id, service_name, window_start, window_end, state, attempts,
			next_attempt_at, expires_at, last_error
		) VALUES ($1,$2,$3,$4,'pending',1,$5,$6,$7)
		ON CONFLICT (service_id, window_start, window_end)
		DO UPDATE SET state='pending', attempts=collection_backlog.attempts+1,
			next_attempt_at=EXCLUDED.next_attempt_at, last_error=EXCLUDED.last_error,
			updated_at=CURRENT_TIMESTAMP
	`, service.Id, service.Name, start, end, retryAt,
		time.Now().UTC().Add(retention), alertingSanitizedError(failure))
	if err != nil {
		return fmt.Errorf("enqueue failed collection window: %w", err)
	}
	if recordErr := q.recorder.RecordCollectionFailure(ctx, alerting.CollectionFailure{
		ServiceID: service.Id, ServiceName: service.Name, WindowStart: start, WindowEnd: end,
		Attempt: 1, RetryAt: retryAt, Error: failure,
	}); recordErr != nil {
		return fmt.Errorf("record collection failure: %w", recordErr)
	}
	q.wake(service.Id)
	return nil
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func enqueueFailedWindow(ctx context.Context, executor sqlExecutor, service *config.Service, start, end, retryAt time.Time, retention time.Duration, failure error) error {
	_, err := executor.ExecContext(ctx, `
		INSERT INTO collection_backlog (
			service_id, service_name, window_start, window_end, state, attempts,
			next_attempt_at, expires_at, last_error
		) VALUES ($1,$2,$3,$4,'pending',1,$5,$6,$7)
		ON CONFLICT (service_id, window_start, window_end)
		DO UPDATE SET state='pending', attempts=collection_backlog.attempts+1,
			next_attempt_at=EXCLUDED.next_attempt_at, last_error=EXCLUDED.last_error,
			updated_at=CURRENT_TIMESTAMP
	`, service.Id, service.Name, start, end, retryAt,
		time.Now().UTC().Add(retention), alertingSanitizedError(failure))
	if err != nil {
		return fmt.Errorf("enqueue failed collection window: %w", err)
	}
	return nil
}

func (q *RecoveryQueue) wake(serviceID int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if target := q.targets[serviceID]; target != nil {
		q.signal(target)
	}
}

func (q *RecoveryQueue) signal(target *recoveryTarget) {
	select {
	case target.wake <- struct{}{}:
	default:
	}
}

func (q *RecoveryQueue) run(target *recoveryTarget) {
	defer q.wg.Done()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		next, worked := q.processOne(target)
		if worked {
			continue
		}
		if next <= 0 {
			next = time.Minute
		}
		timer.Reset(next)
		select {
		case <-q.ctx.Done():
			return
		case <-target.stop:
			return
		case <-target.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func (q *RecoveryQueue) processOne(target *recoveryTarget) (time.Duration, bool) {
	target.mu.RLock()
	serviceCopy := *target.service
	collect := target.collect
	target.mu.RUnlock()
	service := &serviceCopy
	now := time.Now().UTC()
	var (
		id                               int64
		start, end, nextAttempt, expires time.Time
		attempts                         int
	)
	err := q.db.QueryRowContext(q.ctx, `
		SELECT id, window_start, window_end, attempts, next_attempt_at, expires_at
		FROM collection_backlog
		WHERE service_id=$1 AND state IN ('pending','collecting')
		ORDER BY window_start
		LIMIT 1
	`, service.Id).Scan(&id, &start, &end, &attempts, &nextAttempt, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		_ = q.recorder.ResolveCollection(q.ctx, service.Id, now)
		return time.Minute, false
	}
	if err != nil {
		slog.Error("failed to read collection recovery backlog", "service_id", service.Id, "error", err)
		return time.Minute, false
	}
	attempt := windowAttempt{
		Window:     collectionWindow{Start: start, End: end},
		Attempts:   attempts + 1,
		ReadyAt:    nextAttempt,
		ExpiresAt:  expires,
		FailingFor: now.Sub(start),
	}
	processor := newWindowProcessor(func() time.Time { return now })
	result := processor.Process(q.ctx, attempt, func(ctx context.Context) error {
		if _, updateErr := q.db.ExecContext(ctx, `UPDATE collection_backlog SET state='collecting', updated_at=$2 WHERE id=$1`, id, now); updateErr != nil {
			slog.Error("failed to mark collection recovery window collecting", "backlog_id", id, "service_id", service.Id, "error", updateErr)
		}
		slog.Info(
			"collecting recovery window",
			"backlog_id", id,
			"service_id", service.Id,
			"start", start,
			"end", end,
			"attempt", attempt.Attempts,
		)
		collectCtx, cancel := context.WithTimeout(ctx, maxDuration(service.Interval, time.Minute))
		defer cancel()
		return collect(collectCtx, service, start, end)
	}, recoveryWindowPolicy{cfg: q.cfg})

	switch result.Outcome {
	case windowDeferred:
		return result.RetryAfter, false
	case windowMonitoringGap:
		if _, updateErr := q.db.ExecContext(q.ctx, `UPDATE collection_backlog SET state='expired', updated_at=$2 WHERE id=$1`, id, now); updateErr != nil {
			slog.Error("failed to expire collection recovery window", "backlog_id", id, "service_id", service.Id, "error", updateErr)
		} else {
			slog.Warn(
				"collection recovery window expired",
				"backlog_id", id,
				"service_id", service.Id,
				"start", start,
				"end", end,
				"attempts", attempts,
			)
		}
		_ = q.recorder.RecordMonitoringFailure(q.ctx, alerting.MonitoringFailure{
			ServiceID: service.Id, ServiceName: service.Name, OccurredAt: now,
			Operation:        "collection_gap",
			Description:      fmt.Sprintf("Metrics for %s through %s could not be recovered within the backlog retention period.", start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)),
			TechnicalDetails: "The historical collection window expired after repeated recovery attempts.",
		})
		_ = q.recorder.InterruptAnomalies(q.ctx, service.Id, now)
		return 0, true
	case windowCompleted:
		_, updateErr := q.db.ExecContext(q.ctx, `
			UPDATE collection_backlog SET state='recovered', updated_at=$2, last_error=NULL WHERE id=$1
		`, id, time.Now().UTC())
		if updateErr != nil {
			slog.Error("failed to mark collection window recovered", "id", id, "error", updateErr)
			return time.Minute, false
		}
		slog.Info(
			"collection recovery window recovered",
			"backlog_id", id,
			"service_id", service.Id,
			"start", start,
			"end", end,
			"attempt", attempt.Attempts,
		)
		return 0, true
	case windowCancelled:
		return 0, false
	case windowFailed:
		slog.Error("collection recovery window failed without retry policy", "backlog_id", id, "service_id", service.Id, "error", result.Err)
		return time.Minute, false
	}
	delay := result.RetryAfter
	retryAt := now.Add(result.RetryAfter)
	_, updateErr := q.db.ExecContext(q.ctx, `
		UPDATE collection_backlog SET state='pending', attempts=$2, next_attempt_at=$3,
			last_error=$4, updated_at=$5 WHERE id=$1
	`, id, attempt.Attempts, retryAt, alertingSanitizedError(result.Err), now)
	if updateErr != nil {
		slog.Error("failed to reschedule collection recovery", "id", id, "error", updateErr)
		return time.Minute, false
	}
	slog.Warn(
		"collection recovery retry scheduled",
		"backlog_id", id,
		"service_id", service.Id,
		"start", start,
		"end", end,
		"attempt", attempt.Attempts,
		"retry_at", retryAt,
		"error", result.Err,
	)
	_ = q.recorder.RecordCollectionFailure(q.ctx, alerting.CollectionFailure{
		ServiceID: service.Id, ServiceName: service.Name,
		WindowStart: start, WindowEnd: end, Attempt: attempt.Attempts, RetryAt: retryAt, Error: result.Err,
	})
	return delay, false
}

type recoveryWindowPolicy struct {
	cfg config.Config
}

func (p recoveryWindowPolicy) Classify(attempt windowAttempt, err error) windowResult {
	switch {
	case err == nil:
		return windowResult{Outcome: windowCompleted}
	case errors.Is(err, errWindowExpired):
		return windowResult{Outcome: windowMonitoringGap, Err: err}
	default:
		return windowResult{
			Outcome:    windowRetry,
			Err:        err,
			RetryAfter: recoveryDelay(p.cfg, attempt.Attempts, attempt.FailingFor),
		}
	}
}

func recoveryDelay(cfg config.Config, attempt int, failingFor time.Duration) time.Duration {
	probeAfter := configuredRecoveryDuration(cfg, "collection_probe_after", defaultProbeAfter)
	if failingFor >= probeAfter {
		return configuredRecoveryDuration(cfg, "collection_probe_interval", defaultProbeInterval)
	}
	delay := time.Second * time.Duration(1<<min(attempt-1, 10))
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func configuredRecoveryDuration(cfg config.Config, key string, fallback time.Duration) time.Duration {
	value, err := cfg.GetConfig(key)
	if err != nil || value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func alertingSanitizedError(err error) string {
	if err == nil {
		return ""
	}
	// Avoid importing presentation details into callers while still bounding
	// accidental giant upstream responses in the backlog table.
	value := err.Error()
	if len(value) > 2000 {
		value = value[:2000]
	}
	return value
}
