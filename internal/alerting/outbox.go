package alerting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
	databaseutil "github.com/uncertaintea-io/weewoo/internal/database"
)

const (
	outboxPollInterval = time.Second
	outboxSendTimeout  = 5 * time.Second
	activeRefreshAfter = 2 * time.Minute
)

type sendAlert func(context.Context, config.Config, AlertingOptions) error

type OutboxDispatcher struct {
	db                    *sql.DB
	cfg                   config.Config
	manager               *Manager
	send                  sendAlert
	ctx                   context.Context
	cancel                context.CancelFunc
	done                  chan struct{}
	stopOnce              sync.Once
	emergencyMu           sync.Mutex
	databaseUnavailable   bool
	databaseOutageStarted time.Time
	alertmanagerEndpoint  string
	sqlite                bool
}

func NewOutboxDispatcher(db *sql.DB, cfg config.Config, manager *Manager) *OutboxDispatcher {
	return newOutboxDispatcher(db, cfg, manager, SendItContext)
}

func newOutboxDispatcher(db *sql.DB, cfg config.Config, manager *Manager, send sendAlert) *OutboxDispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint, _ := cfg.GetConfig(config.AlertmanagerURLConfigKey)
	dispatcher := &OutboxDispatcher{
		db: db, cfg: cfg, manager: manager, send: send,
		ctx: ctx, cancel: cancel, done: make(chan struct{}), alertmanagerEndpoint: endpoint,
		sqlite: databaseutil.IsSQLite(db),
	}
	go dispatcher.run()
	return dispatcher
}

func (d *OutboxDispatcher) Stop() {
	d.stopOnce.Do(d.cancel)
	<-d.done
}

func (d *OutboxDispatcher) run() {
	defer close(d.done)
	poll := time.NewTicker(outboxPollInterval)
	defer poll.Stop()
	maintenance := time.NewTicker(time.Minute)
	defer maintenance.Stop()
	retention := time.NewTicker(24 * time.Hour)
	defer retention.Stop()
	databaseHealth := time.NewTicker(10 * time.Second)
	defer databaseHealth.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-poll.C:
			if err := d.deliverOne(); err != nil && !errors.Is(err, sql.ErrNoRows) {
				slog.Error("failed to process alert outbox", "error", err)
			}
		case <-maintenance.C:
			if err := d.enqueueRefreshes(); err != nil {
				slog.Error("failed to enqueue active alert refreshes", "error", err)
			}
		case <-retention.C:
			if _, err := d.manager.DeleteExpired(d.ctx); err != nil {
				slog.Error("failed to apply alert retention", "error", err)
			}
		case <-databaseHealth.C:
			healthCtx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
			err := d.db.PingContext(healthCtx)
			cancel()
			if err != nil {
				d.reportDatabaseUnavailable(err)
			} else {
				d.reportDatabaseRecovered()
			}
		}
	}
}

func (d *OutboxDispatcher) reportDatabaseUnavailable(databaseErr error) {
	d.emergencyMu.Lock()
	defer d.emergencyMu.Unlock()
	if d.databaseUnavailable {
		return
	}
	d.databaseUnavailable = true
	d.databaseOutageStarted = time.Now().UTC()
	if d.alertmanagerEndpoint == "" {
		slog.Error("PostgreSQL is unavailable and no cached Alertmanager host exists", "error", databaseErr)
		return
	}
	options := databaseEmergencyOptions(d.databaseOutageStarted, time.Time{})
	ctx, cancel := context.WithTimeout(d.ctx, outboxSendTimeout)
	err := sendToAlertmanagerEndpoint(ctx, d.alertmanagerEndpoint, options)
	cancel()
	if err != nil {
		slog.Error("failed to send emergency database alert", "database_error", databaseErr, "alertmanager_error", err)
	}
}

func (d *OutboxDispatcher) reportDatabaseRecovered() {
	d.emergencyMu.Lock()
	defer d.emergencyMu.Unlock()
	if !d.databaseUnavailable {
		return
	}
	now := time.Now().UTC()
	started := d.databaseOutageStarted
	d.databaseUnavailable = false
	d.databaseOutageStarted = time.Time{}
	if d.alertmanagerEndpoint != "" {
		options := databaseEmergencyOptions(started, now)
		ctx, cancel := context.WithTimeout(d.ctx, outboxSendTimeout)
		if err := sendToAlertmanagerEndpoint(ctx, d.alertmanagerEndpoint, options); err != nil {
			slog.Error("failed to resolve emergency database alert", "error", err)
		}
		cancel()
	}
	historyCtx, cancel := context.WithTimeout(d.ctx, outboxSendTimeout)
	err := d.manager.RecordMonitoringFailure(historyCtx, MonitoringFailure{
		ServiceName: "WeeWoo", OccurredAt: started, Operation: "database",
		Description:      "WeeWoo could not persist monitoring state while its database was unavailable.",
		TechnicalDetails: fmt.Sprintf("Database recovered at %s.", now.Format(time.RFC3339)),
	})
	if err == nil {
		err = d.manager.ResolveMonitoring(historyCtx, 0, "database", now)
	}
	cancel()
	if err != nil {
		slog.Error("failed to reconcile database outage history", "error", err)
	}
}

func databaseEmergencyOptions(started, ended time.Time) AlertingOptions {
	return AlertingOptions{
		Service: "WeeWoo", Serverity: SeverityCritical, Indicator: "Database",
		AlertName: "weewoo_database_unavailable", Summary: "WeeWoo database unavailable",
		Description:     "WeeWoo cannot persist alert history, Verdicts, or collection recovery state.",
		Impact:          "Monitoring results may be delayed or incomplete until the database recovers.",
		SuggestedAction: "Restore database connectivity and inspect WeeWoo health.",
		StartsAt:        started, EndsAt: ended,
		Labels: map[string]string{"weewoo_alert_id": "database-unavailable", "alert_type": "monitoring_impaired"},
	}
}

func (d *OutboxDispatcher) deliverOne() error {
	var (
		id      int64
		alertID int64
		body    []byte
		attempt int
	)
	claimQuery := `
		UPDATE alert_outbox
		SET attempts=attempts+1,
		    next_attempt_at=NOW() + INTERVAL '30 seconds'
		WHERE id = (
			SELECT id FROM alert_outbox
			WHERE state='pending' AND next_attempt_at <= NOW()
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, alert_id, payload, attempts
	`
	if d.sqlite {
		claimQuery = `
			UPDATE alert_outbox
			SET attempts=attempts+1,
			    next_attempt_at=datetime(CURRENT_TIMESTAMP, '+30 seconds')
			WHERE id = (
				SELECT id FROM alert_outbox
				WHERE state='pending' AND next_attempt_at <= CURRENT_TIMESTAMP
				ORDER BY id
				LIMIT 1
			)
			RETURNING id, alert_id, payload, attempts
		`
	}
	err := d.db.QueryRowContext(d.ctx, claimQuery).Scan(&id, &alertID, &body, &attempt)
	if err != nil {
		return err
	}
	var payload outboxPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return d.markFailed(id, alertID, attempt, fmt.Errorf("decode outbox payload: %w", err))
	}
	var status string
	var kind string
	if err := d.db.QueryRowContext(d.ctx, `SELECT status, kind FROM alert WHERE id=$1`, alertID).Scan(&status, &kind); err != nil {
		return err
	}
	if status == StatusResolved && payload.EndsAt.IsZero() {
		_, err := d.db.ExecContext(d.ctx, `
			UPDATE alert_outbox SET state='missed', last_error='alert resolved before firing handoff'
			WHERE id=$1
		`, id)
		return err
	}
	if kind == KindHistoricalAnomaly && payload.EndsAt.IsZero() {
		if err := d.manager.ResolveAlert(d.ctx, alertID, "historical_notifications_disabled", time.Now().UTC()); err != nil {
			return fmt.Errorf("retire historical anomaly notification: %w", err)
		}
		return nil
	}
	options := AlertingOptions{
		Service: payload.Service, Serverity: payload.Severity, Indicator: payload.Indicator,
		AlertName: payload.AlertName, Summary: payload.Summary, Description: payload.Description,
		Impact: payload.Impact, SuggestedAction: payload.SuggestedAction,
		TechnicalDetails: payload.TechnicalDetails, GeneratorURL: payload.GeneratorURL,
		StartsAt: payload.StartsAt, EndsAt: payload.EndsAt, Labels: payload.Labels,
	}
	ctx, cancel := context.WithTimeout(d.ctx, outboxSendTimeout)
	err = d.send(ctx, d.cfg, options)
	cancel()
	if err != nil {
		return d.markFailed(id, alertID, attempt, err)
	}
	now := time.Now().UTC()
	tx, err := d.db.BeginTx(d.ctx, nil)
	if err != nil {
		return err
	}
	var events pendingLifecycleEvents
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(d.ctx, `
		UPDATE alert_outbox SET state='delivered', delivered_at=$2, last_error=NULL WHERE id=$1
	`, id, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(d.ctx, `
		UPDATE alert SET alertmanager_state='accepted', alertmanager_error=NULL,
			last_handoff_at=$2, updated_at=$2 WHERE id=$1
	`, alertID, now); err != nil {
		return err
	}
	if err := events.insert(d.ctx, tx, alertID, "alertmanager_accepted", "Alertmanager accepted the alert update", nil, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	events.log(d.ctx)
	return nil
}

func (d *OutboxDispatcher) markFailed(id, alertID int64, attempt int, deliveryErr error) error {
	delay := time.Duration(math.Min(math.Pow(2, float64(attempt)), 300)) * time.Second
	now := time.Now().UTC()
	nextAttempt := now.Add(delay)
	nextAttemptAt := any(nextAttempt)
	if d.sqlite {
		nextAttemptAt = nextAttempt.Format(time.DateTime)
	}
	tx, err := d.db.BeginTx(d.ctx, nil)
	if err == nil {
		_, err = tx.ExecContext(d.ctx, `
			UPDATE alert_outbox SET next_attempt_at=$2, last_error=$3 WHERE id=$1
		`, id, nextAttemptAt, sanitizeDetails(deliveryErr.Error()))
	}
	if err == nil {
		_, err = tx.ExecContext(d.ctx, `
			UPDATE alert SET alertmanager_state='failed', alertmanager_error=$2, updated_at=$3 WHERE id=$1
		`, alertID, sanitizeDetails(deliveryErr.Error()), now)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil {
		return errors.Join(deliveryErr, err)
	}
	return deliveryErr
}

func (d *OutboxDispatcher) enqueueRefreshes() error {
	_, err := d.db.ExecContext(d.ctx, `
		INSERT INTO alert_outbox (alert_id, operation, payload)
		SELECT a.id, 'firing', (
			SELECT payload FROM alert_outbox previous
			WHERE previous.alert_id=a.id AND previous.operation='firing'
			ORDER BY previous.id DESC LIMIT 1
		)
		FROM alert AS a
		WHERE a.status='firing'
		  AND EXISTS (SELECT 1 FROM alert_outbox previous WHERE previous.alert_id=a.id AND previous.operation='firing')
		  AND (a.last_handoff_at IS NULL OR a.last_handoff_at < $1)
		  AND NOT EXISTS (
			SELECT 1 FROM alert_outbox pending
			WHERE pending.alert_id=a.id AND pending.state='pending'
		  )
	`, time.Now().UTC().Add(-activeRefreshAfter))
	return err
}
