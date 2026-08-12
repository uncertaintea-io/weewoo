package alerting

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/ecdf"
)

const (
	defaultCriticalConsecutive = 3
	defaultAlertRetention      = 90 * 24 * time.Hour
	loadTimeOfDayIndicatorID   = 2
	alertEvidenceWindow        = 5 * time.Minute
)

type Manager struct {
	db         *sql.DB
	cfg        config.Config
	now        func() time.Time
	lockClause string
}

func NewManager(db *sql.DB, cfg config.Config, database string) *Manager {
	lockClause := " FOR UPDATE"
	if strings.EqualFold(strings.TrimSpace(database), "sqlite") {
		lockClause = ""
	}
	return &Manager{db: db, cfg: cfg, now: func() time.Time { return time.Now().UTC() }, lockClause: lockClause}
}

func (m *Manager) RecordBaseline(ctx context.Context, serviceID, indicatorID int, timestamp time.Time) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO verdict (service_id, indicator_id, "timestamp", automated_good, pvalue, analysis_state)
		VALUES ($1, $2, $3, NULL, NULL, 'baseline')
		ON CONFLICT (service_id, indicator_id, "timestamp")
		DO UPDATE SET automated_good = NULL, pvalue = NULL, analysis_state = 'baseline'
	`, serviceID, indicatorID, timestamp)
	if err != nil {
		return fmt.Errorf("record baseline verdict: %w", err)
	}
	return nil
}

func (m *Manager) RecordAnalysis(ctx context.Context, outcome AnalysisOutcome) error {
	if outcome.Historical {
		return m.recordHistoricalAnalysis(ctx, outcome)
	}
	if !outcome.Anomalous {
		return m.recordGoodAnalysis(ctx, outcome)
	}
	return m.recordAnomaly(ctx, outcome)
}

// recordHistoricalAnalysis persists ECDF eligibility without opening,
// resolving, or notifying any user-visible condition.
func (m *Manager) recordHistoricalAnalysis(ctx context.Context, outcome AnalysisOutcome) error {
	state := "good"
	if outcome.Anomalous {
		state = "bad"
	}
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO verdict (service_id, indicator_id, "timestamp", automated_good, pvalue, analysis_state)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (service_id, indicator_id, "timestamp")
		DO UPDATE SET automated_good = EXCLUDED.automated_good,
			pvalue = EXCLUDED.pvalue, analysis_state = EXCLUDED.analysis_state
	`, outcome.ServiceID, outcome.IndicatorID, outcome.Timestamp, !outcome.Anomalous, outcome.PValue, state)
	if err != nil {
		return fmt.Errorf("record historical verdict: %w", err)
	}
	return nil
}

func (m *Manager) recordGoodAnalysis(ctx context.Context, outcome AnalysisOutcome) error {
	var events pendingLifecycleEvents
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin good analysis transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verdict (service_id, indicator_id, "timestamp", automated_good, pvalue, analysis_state)
		VALUES ($1, $2, $3, true, $4, 'good')
		ON CONFLICT (service_id, indicator_id, "timestamp")
		DO UPDATE SET automated_good = true, pvalue = EXCLUDED.pvalue, analysis_state = 'good'
	`, outcome.ServiceID, outcome.IndicatorID, outcome.Timestamp, outcome.PValue); err != nil {
		return fmt.Errorf("record good verdict: %w", err)
	}

	if err := m.resolveByKey(
		ctx,
		tx,
		&events,
		monitoringConditionKey(outcome.ServiceID, "anomaly_analysis"),
		"monitoring_recovered",
		outcome.Timestamp,
	); err != nil {
		return err
	}
	if !outcome.Historical {
		key := anomalyConditionKey(outcome.ServiceID, outcome.IndicatorID, false)
		if err := m.resolveByKey(ctx, tx, &events, key, "good_chunk", outcome.Timestamp); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit good analysis transaction: %w", err)
	}
	events.log(ctx)
	return nil
}

func (m *Manager) recordAnomaly(ctx context.Context, outcome AnalysisOutcome) error {
	var events pendingLifecycleEvents
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin anomaly transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verdict (service_id, indicator_id, "timestamp", automated_good, pvalue, analysis_state)
		VALUES ($1, $2, $3, false, $4, 'bad')
		ON CONFLICT (service_id, indicator_id, "timestamp")
		DO UPDATE SET automated_good = false, pvalue = EXCLUDED.pvalue, analysis_state = 'bad'
	`, outcome.ServiceID, outcome.IndicatorID, outcome.Timestamp, outcome.PValue); err != nil {
		return fmt.Errorf("record bad verdict: %w", err)
	}

	if err := m.resolveByKey(
		ctx,
		tx,
		&events,
		monitoringConditionKey(outcome.ServiceID, "anomaly_analysis"),
		"monitoring_recovered",
		outcome.Timestamp,
	); err != nil {
		return err
	}
	occurrenceKey := fmt.Sprintf("chunk:%d:%d:%s", outcome.ServiceID, outcome.IndicatorID, outcome.Timestamp.UTC().Format(time.RFC3339Nano))
	var duplicate bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM alert_occurrence WHERE occurrence_key = $1)`, occurrenceKey).Scan(&duplicate); err != nil {
		return fmt.Errorf("check anomaly occurrence: %w", err)
	}
	if duplicate {
		if err := tx.Commit(); err != nil {
			return err
		}
		events.log(ctx)
		return nil
	}

	conditionKey := anomalyConditionKey(outcome.ServiceID, outcome.IndicatorID, outcome.Historical)
	alertID, count, severity, created, err := m.ensureAnomalyAlert(ctx, tx, conditionKey, outcome)
	if err != nil {
		return err
	}
	evidence, _ := json.Marshal(map[string]any{
		"load": outcome.Load, "pValue": outcome.PValue, "threshold": outcome.Threshold,
		"indicator": outcome.Indicator, "historical": outcome.Historical,
	})
	summary := fmt.Sprintf("Anomalous time chunk detected at %s", outcome.Timestamp.UTC().Format(time.RFC3339))
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO alert_occurrence (
			alert_id, occurrence_key, kind, occurred_at, detected_at,
			service_id, indicator_id, chunk_timestamp, summary, technical_details, evidence
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $4, $8, $9, $10)
	`, alertID, occurrenceKey, anomalyKind(outcome.Historical), outcome.Timestamp, m.now(),
		outcome.ServiceID, outcome.IndicatorID, summary, sanitizeDetails(outcome.TechnicalDetails), evidence); err != nil {
		return fmt.Errorf("insert anomaly occurrence: %w", err)
	}

	description := anomalyDescription(outcome.ServiceName, count, outcome.Historical)
	if _, err := tx.ExecContext(ctx, `
		UPDATE alert
		SET severity = $2, description = $3, last_occurred_at = $4,
		    occurrence_count = $5, consecutive_count = $5,
		    revision = revision + 1, updated_at = $6
		WHERE id = $1
	`, alertID, severity, description, outcome.Timestamp, count, m.now()); err != nil {
		return fmt.Errorf("update anomaly alert: %w", err)
	}

	eventType := "occurrence_added"
	message := summary
	if created {
		eventType = "opened"
		message = "Anomaly condition opened"
	} else if severity == SeverityCritical && count == m.criticalThreshold("alert_critical_consecutive") {
		eventType = "escalated"
		message = fmt.Sprintf("Escalated to critical after %d consecutive anomalous time chunks", count)
	}
	if err := events.insert(ctx, tx, alertID, eventType, message, nil, m.now()); err != nil {
		return err
	}
	alert, err := readAlertForPayload(ctx, tx, alertID)
	if err != nil {
		return err
	}
	alert.GeneratorURL = outcome.GeneratorURL
	if severity == SeverityCritical && count == m.criticalThreshold("alert_critical_consecutive") {
		if err := retireDeliveredSeverity(ctx, tx, alertID, SeverityWarning, outcome.Timestamp); err != nil {
			return err
		}
	}
	if err := insertOutbox(ctx, tx, alertID, "firing", alert, m.now()); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit anomaly transaction: %w", err)
	}
	events.log(ctx)
	return nil
}

func (m *Manager) ensureAnomalyAlert(ctx context.Context, tx *sql.Tx, conditionKey string, outcome AnalysisOutcome) (int64, int, string, bool, error) {
	var id int64
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT id, occurrence_count
		FROM alert
		WHERE condition_key = $1 AND status = 'firing'`+m.lockClause,
		conditionKey).Scan(&id, &count)
	if err == nil {
		count++
		return id, count, severityForCount(count, m.criticalThreshold("alert_critical_consecutive")), false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, "", false, fmt.Errorf("read anomaly alert: %w", err)
	}

	kind := anomalyKind(outcome.Historical)
	title := "Anomalous service behavior detected"
	impact := "The observed latency distribution differed significantly from the learned reference."
	action := "Inspect the service and the affected time chunks. Accept a chunk as normal only when it represents expected behavior."
	if outcome.Historical {
		title = "Historical anomaly detected after collection recovery"
		impact = "Recovered metrics show unusual past behavior; this does not by itself mean the service is currently unhealthy."
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO alert (
			condition_key, service_id, service_name, indicator_id, kind,
			severity, status, title, description, impact, suggested_action,
			technical_details, started_at, last_occurred_at
		) VALUES ($1, $2, $3, $4, $5, 'warning', 'firing', $6, $7, $8, $9, $10, $11, $11)
		RETURNING id
	`, conditionKey, outcome.ServiceID, outcome.ServiceName, outcome.IndicatorID, kind,
		title, anomalyDescription(outcome.ServiceName, 1, outcome.Historical), impact, action,
		sanitizeDetails(outcome.TechnicalDetails), outcome.Timestamp).Scan(&id)
	if err != nil {
		return 0, 0, "", false, fmt.Errorf("insert anomaly alert: %w", err)
	}
	return id, 1, SeverityWarning, true, nil
}

func (m *Manager) RecordAnalysisFailure(ctx context.Context, outcome AnalysisOutcome, failure error) error {
	if outcome.Historical {
		_, err := m.db.ExecContext(ctx, `
			INSERT INTO verdict (service_id, indicator_id, "timestamp", automated_good, pvalue, analysis_state)
			VALUES ($1, $2, $3, NULL, NULL, 'failed')
			ON CONFLICT (service_id, indicator_id, "timestamp")
			DO UPDATE SET automated_good = NULL, pvalue = NULL, analysis_state = 'failed'
		`, outcome.ServiceID, outcome.IndicatorID, outcome.Timestamp)
		if err != nil {
			return fmt.Errorf("record failed historical analysis state: %w", err)
		}
		return nil
	}
	var events pendingLifecycleEvents
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin analysis failure transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, verdictErr := tx.ExecContext(ctx, `
		INSERT INTO verdict (service_id, indicator_id, "timestamp", automated_good, pvalue, analysis_state)
		VALUES ($1, $2, $3, NULL, NULL, 'failed')
		ON CONFLICT (service_id, indicator_id, "timestamp")
		DO UPDATE SET automated_good = NULL, pvalue = NULL, analysis_state = 'failed'
	`, outcome.ServiceID, outcome.IndicatorID, outcome.Timestamp)
	if verdictErr != nil {
		return fmt.Errorf("record failed analysis state: %w", verdictErr)
	}
	if err := m.recordMonitoringFailureTx(ctx, tx, &events, MonitoringFailure{
		ServiceID: outcome.ServiceID, ServiceName: outcome.ServiceName, IndicatorID: outcome.IndicatorID,
		OccurredAt: outcome.Timestamp, Operation: "anomaly_analysis",
		Description:      "WeeWoo collected metrics but could not complete anomaly analysis. Service behavior for this interval is unknown.",
		TechnicalDetails: sanitizeDetails(failure.Error()), GeneratorURL: outcome.GeneratorURL,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit analysis failure transaction: %w", err)
	}
	events.log(ctx)
	return nil
}

func (m *Manager) RecordCollectionFailure(ctx context.Context, failure CollectionFailure) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin collection failure transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	logCommittedEvents, err := m.RecordCollectionFailureTx(ctx, tx, failure)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit collection failure transaction: %w", err)
	}
	logCommittedEvents()
	return nil
}

func (m *Manager) RecordCollectionFailureTx(ctx context.Context, tx *sql.Tx, failure CollectionFailure) (func(), error) {
	var events pendingLifecycleEvents
	key := fmt.Sprintf("collection:%d", failure.ServiceID)
	var alertID int64
	var count int
	created := false
	err := tx.QueryRowContext(ctx, `
		SELECT id, consecutive_count FROM alert
		WHERE condition_key = $1 AND status = 'firing'`+m.lockClause,
		key).Scan(&alertID, &count)
	if errors.Is(err, sql.ErrNoRows) {
		created = true
		err = tx.QueryRowContext(ctx, `
			INSERT INTO alert (
				condition_key, service_id, service_name, kind, severity, status,
				title, description, impact, suggested_action, technical_details,
				started_at, last_occurred_at
			) VALUES ($1, $2, $3, $4, 'warning', 'firing',
				'Metrics collection failing', '',
				'WeeWoo cannot assess service behavior for windows whose metrics are unavailable.',
				'Check Prometheus availability and the configured queries.',
				$5, $6, $7)
			RETURNING id
		`, key, failure.ServiceID, failure.ServiceName, KindCollectionFailure,
			sanitizeError(failure.Error), failure.WindowStart, failure.WindowEnd).Scan(&alertID)
		count = 0
	}
	if err != nil {
		return nil, fmt.Errorf("read or create collection alert: %w", err)
	}
	count++
	severity := severityForCount(count, m.criticalThreshold("collection_critical_consecutive"))
	description := collectionDescription(failure.ServiceName, count, failure.RetryAt)
	if _, err := tx.ExecContext(ctx, `
		UPDATE alert SET severity=$2, description=$3, technical_details=$4,
			last_occurred_at=$5, occurrence_count=occurrence_count+1,
			consecutive_count=$6, revision=revision+1, updated_at=$7
		WHERE id=$1
	`, alertID, severity, description, sanitizeError(failure.Error), failure.WindowEnd, count, m.now()); err != nil {
		return nil, fmt.Errorf("update collection alert: %w", err)
	}
	occurrenceKey := fmt.Sprintf("collection:%d:%s:%s:%d", failure.ServiceID,
		failure.WindowStart.UTC().Format(time.RFC3339Nano), failure.WindowEnd.UTC().Format(time.RFC3339Nano), failure.Attempt)
	evidence, _ := json.Marshal(map[string]any{"attempt": failure.Attempt, "retryAt": failure.RetryAt})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO alert_occurrence (
			alert_id, occurrence_key, kind, occurred_at, window_start, window_end,
			service_id, summary, technical_details, evidence
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (occurrence_key) DO NOTHING
	`, alertID, occurrenceKey, KindCollectionFailure, m.now(), failure.WindowStart, failure.WindowEnd,
		failure.ServiceID, "Prometheus metrics collection failed", sanitizeError(failure.Error), evidence); err != nil {
		return nil, fmt.Errorf("insert collection occurrence: %w", err)
	}
	eventType, message := "occurrence_added", "Collection retry failed"
	if created {
		eventType, message = "opened", "Collection failure condition opened"
	} else if severity == SeverityCritical && count == m.criticalThreshold("collection_critical_consecutive") {
		eventType, message = "escalated", fmt.Sprintf("Escalated to critical after %d consecutive collection failures", count)
	}
	if err := events.insert(ctx, tx, alertID, eventType, message, nil, m.now()); err != nil {
		return nil, err
	}
	payload, err := readAlertForPayload(ctx, tx, alertID)
	if err != nil {
		return nil, err
	}
	payload.GeneratorURL = failure.GeneratorURL
	if severity == SeverityCritical && count == m.criticalThreshold("collection_critical_consecutive") {
		if err := retireDeliveredSeverity(ctx, tx, alertID, SeverityWarning, m.now()); err != nil {
			return nil, err
		}
	}
	if err := insertOutbox(ctx, tx, alertID, "firing", payload, m.now()); err != nil {
		return nil, err
	}
	return func() { events.log(ctx) }, nil
}

func (m *Manager) ResolveCollection(ctx context.Context, serviceID int, at time.Time) error {
	return m.resolveCondition(ctx, fmt.Sprintf("collection:%d", serviceID), "collection_recovered", at)
}

func (m *Manager) RecordMonitoringFailure(ctx context.Context, failure MonitoringFailure) error {
	var events pendingLifecycleEvents
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := m.recordMonitoringFailureTx(ctx, tx, &events, failure); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	events.log(ctx)
	return nil
}

func (m *Manager) recordMonitoringFailureTx(ctx context.Context, tx *sql.Tx, events *pendingLifecycleEvents, failure MonitoringFailure) error {
	key := monitoringConditionKey(failure.ServiceID, failure.Operation)
	var id int64
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT id, occurrence_count
		FROM alert
		WHERE condition_key=$1 AND status='firing'`+m.lockClause,
		key).Scan(&id, &count)
	if errors.Is(err, sql.ErrNoRows) {
		count = 1
		err = tx.QueryRowContext(ctx, `
			INSERT INTO alert (
				condition_key, service_id, service_name, indicator_id, kind, severity, status,
				title, description, impact, suggested_action, technical_details,
				started_at, last_occurred_at, occurrence_count
			) VALUES ($1,$2,$3,$4,$5,'warning','firing','Monitoring impaired',$6,
				'WeeWoo cannot reliably assess the service while this operation is failing.',
				'Inspect WeeWoo and Prometheus health; collected data remains excluded until analysis succeeds.',
				$7,$8,$8,1) RETURNING id
		`, key, failure.ServiceID, failure.ServiceName, nullableInt(failure.IndicatorID),
			KindMonitoringImpaired, failure.Description, sanitizeDetails(failure.TechnicalDetails), failure.OccurredAt).Scan(&id)
	} else if err == nil {
		count++
		severity := severityForCount(count, m.criticalThreshold("monitoring_critical_consecutive"))
		_, err = tx.ExecContext(ctx, `
			UPDATE alert SET last_occurred_at=$2, occurrence_count=occurrence_count+1,
				severity=$3, description=$4, technical_details=$5, revision=revision+1,
				updated_at=$6 WHERE id=$1
		`, id, failure.OccurredAt, severity, failure.Description,
			sanitizeDetails(failure.TechnicalDetails), m.now())
	}
	if err != nil {
		return fmt.Errorf("record monitoring alert: %w", err)
	}
	occurrenceKey := fmt.Sprintf("monitoring:%d:%s:%s", failure.ServiceID, failure.Operation, failure.OccurredAt.UTC().Format(time.RFC3339Nano))
	_, err = tx.ExecContext(ctx, `
		INSERT INTO alert_occurrence (alert_id, occurrence_key, kind, occurred_at, service_id, indicator_id, summary, technical_details)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (occurrence_key) DO NOTHING
	`, id, occurrenceKey, KindMonitoringImpaired, failure.OccurredAt, failure.ServiceID,
		nullableInt(failure.IndicatorID), failure.Description, sanitizeDetails(failure.TechnicalDetails))
	if err != nil {
		return fmt.Errorf("record monitoring occurrence: %w", err)
	}
	if err := events.insert(ctx, tx, id, "occurrence_added", failure.Description, nil, m.now()); err != nil {
		return err
	}
	criticalAt := m.criticalThreshold("monitoring_critical_consecutive")
	if count == criticalAt {
		if err := events.insert(ctx, tx, id, "severity_changed", "Monitoring impairment escalated to critical.", map[string]any{"severity": SeverityCritical}, m.now()); err != nil {
			return err
		}
		if err := retireDeliveredSeverity(ctx, tx, id, SeverityWarning, m.now()); err != nil {
			return err
		}
	}
	payload, err := readAlertForPayload(ctx, tx, id)
	if err != nil {
		return err
	}
	payload.GeneratorURL = failure.GeneratorURL
	if err := insertOutbox(ctx, tx, id, "firing", payload, m.now()); err != nil {
		return err
	}
	return nil
}

func (m *Manager) ResolveMonitoring(ctx context.Context, serviceID int, operation string, at time.Time) error {
	return m.resolveCondition(ctx, monitoringConditionKey(serviceID, operation), "monitoring_recovered", at)
}

// InterruptAnomalies closes only live anomaly conditions when monitoring has
// developed a permanent gap. It does not imply that collection itself
// recovered and leaves unrelated conditions firing.
func (m *Manager) InterruptAnomalies(ctx context.Context, serviceID int, at time.Time) error {
	var events pendingLifecycleEvents
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM alert
		WHERE service_id=$1 AND kind=$2 AND status='firing'`+m.lockClause,
		serviceID, KindAnomaly)
	if err != nil {
		return fmt.Errorf("read anomalies interrupted by monitoring gap: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := m.resolveAlert(ctx, tx, &events, id, "monitoring_interrupted", at); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	events.log(ctx)
	return nil
}

func (m *Manager) CloseService(ctx context.Context, serviceID int, reason string, at time.Time) error {
	var events pendingLifecycleEvents
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM alert WHERE service_id=$1 AND status='firing'`+m.lockClause, serviceID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := m.resolveAlert(ctx, tx, &events, id, reason, at); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	events.log(ctx)
	return nil
}

func (m *Manager) resolveCondition(ctx context.Context, key, reason string, at time.Time) error {
	var events pendingLifecycleEvents
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := m.resolveByKey(ctx, tx, &events, key, reason, at); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	events.log(ctx)
	return nil
}

func (m *Manager) ResolveAlert(ctx context.Context, id int64, reason string, at time.Time) error {
	var events pendingLifecycleEvents
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM alert WHERE id=$1`+m.lockClause, id).Scan(&status); err != nil {
		return err
	}
	if status == StatusResolved {
		return tx.Commit()
	}
	if err := m.resolveAlert(ctx, tx, &events, id, reason, at); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	events.log(ctx)
	return nil
}

func (m *Manager) resolveByKey(ctx context.Context, tx *sql.Tx, events *pendingLifecycleEvents, key, reason string, at time.Time) error {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM alert WHERE condition_key=$1 AND status='firing'`+m.lockClause, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read firing alert for resolution: %w", err)
	}
	return m.resolveAlert(ctx, tx, events, id, reason, at)
}

func (m *Manager) resolveAlert(ctx context.Context, tx *sql.Tx, events *pendingLifecycleEvents, id int64, reason string, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE alert SET status='resolved', resolved_at=$2, resolution_reason=$3,
			retention_anchor=$2, revision=revision+1, updated_at=$2 WHERE id=$1
	`, id, at, reason); err != nil {
		return fmt.Errorf("resolve alert: %w", err)
	}
	if err := events.insert(ctx, tx, id, "resolved", resolutionMessage(reason), map[string]any{"reason": reason}, at); err != nil {
		return err
	}
	var delivered bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM alert_outbox WHERE alert_id=$1 AND operation='firing' AND state='delivered'
		)
	`, id).Scan(&delivered); err != nil {
		return fmt.Errorf("check firing delivery: %w", err)
	}
	if !delivered {
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert_outbox SET state='missed', last_error='resolved before Alertmanager handoff'
			WHERE alert_id=$1 AND operation='firing' AND state='pending'
		`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE alert SET alertmanager_state='missed' WHERE id=$1`, id)
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE alert_outbox SET state='missed', last_error='alert resolved before pending firing handoff'
		WHERE alert_id=$1 AND operation='firing' AND state='pending'
	`, id); err != nil {
		return err
	}
	return insertDeliveredResolutions(ctx, tx, id, at)
}

func (m *Manager) ReviewOccurrence(ctx context.Context, occurrenceID, expectedRevision int64, accept bool, reason string) (ReviewResult, error) {
	var events pendingLifecycleEvents
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var alertID int64
	var revision int64
	var current sql.NullBool
	var serviceID, indicatorID int
	var timestamp time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT alert_id, review_revision, review_override, service_id, indicator_id, chunk_timestamp
		FROM alert_occurrence WHERE id=$1`+m.lockClause,
		occurrenceID).Scan(&alertID, &revision, &current, &serviceID, &indicatorID, &timestamp)
	if err != nil {
		return ReviewResult{}, fmt.Errorf("read review occurrence: %w", err)
	}
	if current.Valid && current.Bool == accept {
		var reviewedAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT reviewed_at FROM alert_occurrence WHERE id=$1`, occurrenceID).Scan(&reviewedAt); err != nil {
			return ReviewResult{}, err
		}
		return ReviewResult{OccurrenceID: occurrenceID, Revision: revision, Accepted: accept, ReviewedAt: reviewedAt}, tx.Commit()
	}
	if revision != expectedRevision {
		return ReviewResult{}, ErrReviewConflict
	}
	now := m.now()
	newRevision := revision + 1
	if _, err := tx.ExecContext(ctx, `
		UPDATE alert_occurrence SET review_override=$2, review_revision=$3, reviewed_at=$4, review_reason=$5
		WHERE id=$1
	`, occurrenceID, accept, newRevision, now, strings.TrimSpace(reason)); err != nil {
		return ReviewResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE verdict SET review_override=$4, review_revision=$5, reviewed_at=$6, review_reason=$7
		WHERE service_id=$1 AND indicator_id=$2 AND "timestamp"=$3
	`, serviceID, indicatorID, timestamp, accept, newRevision, now, strings.TrimSpace(reason)); err != nil {
		return ReviewResult{}, err
	}
	eventType, message := "review_reverted", "Automated Verdict restored"
	if accept {
		eventType, message = "accepted_as_normal", "Bad chunk accepted as normal for future reference builds"
	}
	if err := events.insert(ctx, tx, alertID, eventType, message, map[string]any{"occurrenceId": occurrenceID, "reason": reason}, now); err != nil {
		return ReviewResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE alert SET retention_anchor = CASE WHEN status='resolved' THEN $2 ELSE retention_anchor END,
			revision=revision+1, updated_at=$2 WHERE id=$1
	`, alertID, now); err != nil {
		return ReviewResult{}, err
	}
	var status, resolutionReason string
	if err := tx.QueryRowContext(ctx, `
		SELECT status, COALESCE(resolution_reason,'') FROM alert WHERE id=$1
	`, alertID).Scan(&status, &resolutionReason); err != nil {
		return ReviewResult{}, err
	}
	firing := status == StatusFiring
	resolved := false
	if firing && accept {
		var remaining int
		if err := tx.QueryRowContext(ctx, `
			SELECT count(*) FROM alert_occurrence
			WHERE alert_id=$1 AND chunk_timestamp IS NOT NULL AND review_override IS DISTINCT FROM true
		`, alertID).Scan(&remaining); err != nil {
			return ReviewResult{}, err
		}
		if remaining == 0 {
			if err := m.resolveAlert(ctx, tx, &events, alertID, "accepted_as_normal", now); err != nil {
				return ReviewResult{}, err
			}
			resolved = true
		}
	}
	if !accept && status == StatusResolved && resolutionReason == "accepted_as_normal" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE alert SET status='firing', resolved_at=NULL, resolution_reason=NULL,
				retention_anchor=NULL, revision=revision+1, updated_at=$2 WHERE id=$1
		`, alertID, now); err != nil {
			return ReviewResult{}, err
		}
		if err := events.insert(ctx, tx, alertID, "reopened", "Anomaly condition reopened after restoring the automated Verdict", nil, now); err != nil {
			return ReviewResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO alert_outbox (alert_id, operation, payload, next_attempt_at)
			SELECT $1, 'firing', payload, $2
			FROM alert_outbox
			WHERE alert_id=$1 AND operation='firing'
			ORDER BY id DESC LIMIT 1
		`, alertID, now); err != nil {
			return ReviewResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ReviewResult{}, err
	}
	events.log(ctx)
	return ReviewResult{OccurrenceID: occurrenceID, Revision: newRevision, Accepted: accept, ReviewedAt: now, AlertResolved: resolved}, nil
}

func (m *Manager) List(ctx context.Context, includeResolved bool, limit int) ([]Alert, error) {
	query := `
		SELECT id, service_id, service_name, indicator_id, kind, severity, status,
			title, description, impact, suggested_action, technical_details,
			started_at, last_occurred_at, resolved_at, COALESCE(resolution_reason,''),
			occurrence_count, consecutive_count, alertmanager_state, COALESCE(alertmanager_error,'')
		FROM alert`
	args := []any{}
	if !includeResolved {
		query += ` WHERE status='firing'`
	}
	query += ` ORDER BY (status='firing') DESC, last_occurred_at DESC`
	if limit > 0 {
		if limit > 500 {
			limit = 500
		}
		query += ` LIMIT $1`
		args = append(args, limit)
	}
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()
	alerts := make([]Alert, 0)
	for rows.Next() {
		var item Alert
		var serviceID, indicatorID sql.NullInt64
		var resolvedAt sql.NullTime
		if err := rows.Scan(
			&item.ID, &serviceID, &item.ServiceName, &indicatorID, &item.Kind,
			&item.Severity, &item.Status, &item.Title, &item.Description, &item.Impact,
			&item.SuggestedAction, &item.TechnicalDetails, &item.StartedAt,
			&item.LastOccurredAt, &resolvedAt, &item.ResolutionReason,
			&item.OccurrenceCount, &item.ConsecutiveCount, &item.AlertmanagerState,
			&item.AlertmanagerError,
		); err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		if serviceID.Valid {
			value := int(serviceID.Int64)
			item.ServiceID = &value
		}
		if indicatorID.Valid {
			value := int(indicatorID.Int64)
			item.IndicatorID = &value
		}
		if resolvedAt.Valid {
			item.ResolvedAt = &resolvedAt.Time
		}
		alerts = append(alerts, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range alerts {
		occurrences, err := m.listOccurrences(ctx, alerts[index].ID)
		if err != nil {
			return nil, err
		}
		events, err := m.listEvents(ctx, alerts[index].ID)
		if err != nil {
			return nil, err
		}
		alerts[index].Occurrences = occurrences
		alerts[index].Events = events
	}
	return alerts, nil
}

// GetEvidence returns the reference distribution, observations, and test result that explain an anomaly Occurrence.
func (m *Manager) GetEvidence(ctx context.Context, occurrenceID int64) (AlertEvidence, error) {
	var kind string
	var serviceID, indicatorID sql.NullInt64
	var chunkTimestamp sql.NullTime
	err := m.db.QueryRowContext(ctx, `
		SELECT o.service_id, o.indicator_id, o.chunk_timestamp, o.kind
		FROM alert_occurrence AS o
		WHERE o.id=$1
	`, occurrenceID).Scan(&serviceID, &indicatorID, &chunkTimestamp, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return AlertEvidence{}, ErrOccurrenceNotFound
	}
	if err != nil {
		return AlertEvidence{}, fmt.Errorf("read Alert Evidence: %w", err)
	}
	if kind != KindAnomaly {
		return AlertEvidence{}, ErrEvidenceNotApplicable
	}
	if !serviceID.Valid || !indicatorID.Valid || !chunkTimestamp.Valid {
		return AlertEvidence{}, fmt.Errorf("anomaly occurrence %d has no time chunk identity", occurrenceID)
	}

	service := int(serviceID.Int64)
	indicator := int(indicatorID.Int64)
	primary := chunkTimestamp.Time
	var generation, activeGeneration int64
	var pValue sql.NullFloat64
	err = m.db.QueryRowContext(ctx, `
		SELECT tc.generation, v.pvalue, s.generation
		FROM time_chunk AS tc
		JOIN verdict AS v
		  ON v.service_id=tc.service_id AND v.indicator_id=tc.indicator_id
		 AND v."timestamp"=tc."timestamp" AND v.generation=tc.generation
		JOIN service AS s ON s.id=tc.service_id
		WHERE tc.service_id=$1 AND tc.indicator_id=$2
		  AND tc."timestamp"=$3
	`, service, indicator, primary).Scan(&generation, &pValue, &activeGeneration)
	if err != nil {
		return AlertEvidence{}, fmt.Errorf("read occurrence time chunk verdict: %w", err)
	}
	if !pValue.Valid {
		return AlertEvidence{}, fmt.Errorf("anomaly occurrence %d has no KS-test p-value", occurrenceID)
	}
	if generation != activeGeneration {
		return AlertEvidence{}, ErrEvidenceReferenceGone
	}

	xSamples, ySamples, err := m.readEvidenceSamples(ctx, service, indicator, generation, primary)
	if err != nil {
		return AlertEvidence{}, err
	}
	input, err := weightedAverage(xSamples)
	if err != nil {
		return AlertEvidence{}, fmt.Errorf("calculate Alert Evidence query input: %w", err)
	}
	samples, err := aggregateSamples(ySamples)
	if err != nil {
		return AlertEvidence{}, fmt.Errorf("aggregate Alert Evidence samples: %w", err)
	}

	joint, _, err := ecdf.NewDatabaseJointStore(m.db).ReadCurrent(ctx, service, indicator)
	if errors.Is(err, sql.ErrNoRows) {
		return AlertEvidence{}, ErrEvidenceReferenceGone
	}
	if err != nil {
		return AlertEvidence{}, fmt.Errorf("read occurrence joint ECDF: %w", err)
	}
	// A generation reset deletes every published ECDF for the service. Recheck
	// after the read so a concurrent reset cannot pair an old chunk with a newly
	// published reference from another generation.
	if err := m.db.QueryRowContext(ctx, `SELECT generation FROM service WHERE id=$1`, service).Scan(&activeGeneration); err != nil {
		return AlertEvidence{}, fmt.Errorf("re-read service generation: %w", err)
	}
	if generation != activeGeneration {
		return AlertEvidence{}, ErrEvidenceReferenceGone
	}
	xs, ps, err := ecdf.Query(ctx, joint, input)
	if err != nil {
		return AlertEvidence{}, fmt.Errorf("query occurrence joint ECDF: %w", err)
	}
	if len(xs) == 0 {
		return AlertEvidence{}, fmt.Errorf("query occurrence joint ECDF returned no points")
	}
	return AlertEvidence{
		Query:   AlertQueryResult{Input: input, Xs: xs, Ps: ps},
		Samples: samples,
		PValue:  pValue.Float64,
	}, nil
}

func (m *Manager) readEvidenceSamples(ctx context.Context, serviceID, indicatorID int, generation int64, primary time.Time) ([][]ecdf.Sample, [][]ecdf.Sample, error) {
	start := primary
	if indicatorID == loadTimeOfDayIndicatorID {
		start = primary.Add(-alertEvidenceWindow)
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT chunk
		FROM time_chunk
		WHERE service_id=$1 AND indicator_id=$2 AND generation=$3
		  AND "timestamp">=$4 AND "timestamp"<=$5
		ORDER BY "timestamp"
	`, serviceID, indicatorID, generation, start, primary)
	if err != nil {
		return nil, nil, fmt.Errorf("read occurrence time chunks: %w", err)
	}
	defer rows.Close()
	var xs, ys [][]ecdf.Sample
	for rows.Next() {
		var chunk []byte
		if err := rows.Scan(&chunk); err != nil {
			return nil, nil, fmt.Errorf("scan occurrence time chunk: %w", err)
		}
		_, x, y, err := ecdf.Decode(chunk)
		if err != nil {
			return nil, nil, fmt.Errorf("decode occurrence time chunk: %w", err)
		}
		xs = append(xs, x)
		ys = append(ys, y)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate occurrence time chunks: %w", err)
	}
	if len(xs) == 0 {
		return nil, nil, fmt.Errorf("occurrence time chunks are unavailable")
	}
	return xs, ys, nil
}

func weightedAverage(groups [][]ecdf.Sample) (float64, error) {
	var total, weight float64
	for _, samples := range groups {
		for _, sample := range samples {
			if sample.Count == 0 || math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) {
				return 0, fmt.Errorf("invalid independent-variable sample")
			}
			count := float64(sample.Count)
			total = math.FMA(sample.Value, count, total)
			weight += count
		}
	}
	if weight == 0 || math.IsInf(total, 0) || math.IsNaN(total) {
		return 0, fmt.Errorf("no finite independent-variable observations")
	}
	return total / weight, nil
}

func aggregateSamples(groups [][]ecdf.Sample) ([]ecdf.Sample, error) {
	switch len(groups) {
	case 0:
		return []ecdf.Sample{}, nil
	case 1:
		return groups[0], nil
	}

	// Start with the first group, in sorted order, and merge the remaining groups into it.
	result := slices.Clone(groups[0])
	sortFunc := func(a, b ecdf.Sample) int {
		return cmp.Compare(a.Value, b.Value)
	}
	slices.SortFunc(result, sortFunc)
	for _, samples := range groups[1:] {
		for _, sample := range samples {
			i, found := slices.BinarySearchFunc(result, sample, sortFunc)
			if found {
				if ^uint64(0)-result[i].Count < sample.Count {
					return nil, fmt.Errorf("dependent-variable sample count overflows uint64")
				}
				result[i].Count += sample.Count
				continue
			}
			result = slices.Insert(result, i, sample)
		}
	}
	return result, nil
}

func (m *Manager) listOccurrences(ctx context.Context, alertID int64) ([]Occurrence, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, kind, occurred_at, detected_at, window_start, window_end,
			chunk_timestamp, summary, technical_details, evidence,
			review_revision, review_override, reviewed_at, COALESCE(review_reason,'')
		FROM alert_occurrence WHERE alert_id=$1 ORDER BY occurred_at DESC
	`, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Occurrence, 0)
	for rows.Next() {
		var item Occurrence
		var start, end, chunk, reviewed sql.NullTime
		var review sql.NullBool
		var evidence []byte
		if err := rows.Scan(&item.ID, &item.Kind, &item.OccurredAt, &item.DetectedAt,
			&start, &end, &chunk, &item.Summary, &item.TechnicalDetails, &evidence,
			&item.ReviewRevision, &review, &reviewed, &item.ReviewReason); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(evidence, &item.Evidence)
		if item.Evidence == nil {
			item.Evidence = map[string]any{}
		}
		if start.Valid {
			item.WindowStart = &start.Time
		}
		if end.Valid {
			item.WindowEnd = &end.Time
		}
		if chunk.Valid {
			item.ChunkTimestamp = &chunk.Time
		}
		if review.Valid {
			value := review.Bool
			item.ReviewOverride = &value
		}
		if reviewed.Valid {
			item.ReviewedAt = &reviewed.Time
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (m *Manager) listEvents(ctx context.Context, alertID int64) ([]Event, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT type, message, metadata, occurred_at
		FROM alert_event WHERE alert_id=$1 ORDER BY occurred_at DESC
	`, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Event, 0)
	for rows.Next() {
		var item Event
		var metadata []byte
		if err := rows.Scan(&item.Type, &item.Message, &metadata, &item.OccurredAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metadata, &item.Metadata)
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (m *Manager) DeleteExpired(ctx context.Context) (int64, error) {
	retention := m.configuredDuration("alert_retention", defaultAlertRetention)
	result, err := m.db.ExecContext(ctx, `
		DELETE FROM alert
		WHERE status='resolved' AND retention_anchor < $1
	`, m.now().Add(-retention))
	if err != nil {
		return 0, fmt.Errorf("delete expired alerts: %w", err)
	}
	return result.RowsAffected()
}

type outboxPayload struct {
	AlertID          int64             `json:"alertId"`
	Service          string            `json:"service"`
	Indicator        string            `json:"indicator"`
	AlertName        string            `json:"alertName"`
	Severity         string            `json:"severity"`
	Summary          string            `json:"summary"`
	Description      string            `json:"description"`
	Impact           string            `json:"impact"`
	SuggestedAction  string            `json:"suggestedAction"`
	TechnicalDetails string            `json:"technicalDetails"`
	GeneratorURL     string            `json:"generatorUrl"`
	StartsAt         time.Time         `json:"startsAt"`
	EndsAt           time.Time         `json:"endsAt,omitempty"`
	Labels           map[string]string `json:"labels"`
}

func readAlertForPayload(ctx context.Context, tx *sql.Tx, id int64) (outboxPayload, error) {
	var payload outboxPayload
	var indicatorID sql.NullInt64
	var kind string
	err := tx.QueryRowContext(ctx, `
		SELECT id, service_name, indicator_id, kind, severity, title, description,
			impact, suggested_action, technical_details, started_at
		FROM alert WHERE id=$1
	`, id).Scan(&payload.AlertID, &payload.Service, &indicatorID, &kind,
		&payload.Severity, &payload.Summary, &payload.Description, &payload.Impact,
		&payload.SuggestedAction, &payload.TechnicalDetails, &payload.StartsAt)
	if err != nil {
		return payload, fmt.Errorf("read alert outbox payload: %w", err)
	}
	payload.AlertName = kind
	if indicatorID.Valid {
		payload.Indicator = strconv.FormatInt(indicatorID.Int64, 10)
	}
	payload.Labels = map[string]string{
		"weewoo_alert_id": strconv.FormatInt(payload.AlertID, 10),
		"alert_type":      kind,
	}
	return payload, nil
}

func insertOutbox(ctx context.Context, tx *sql.Tx, alertID int64, operation string, payload outboxPayload, at time.Time) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO alert_outbox (alert_id, operation, payload, next_attempt_at)
		VALUES ($1,$2,$3,$4)
	`, alertID, operation, body, at); err != nil {
		return fmt.Errorf("insert alert outbox: %w", err)
	}
	return nil
}

func insertDeliveredResolutions(ctx context.Context, tx *sql.Tx, alertID int64, at time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT payload
		FROM alert_outbox
		WHERE alert_id=$1 AND operation='firing' AND state='delivered'
		ORDER BY id
	`, alertID)
	if err != nil {
		return fmt.Errorf("read delivered alert forms: %w", err)
	}
	payloads := make(map[string]outboxPayload)
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			_ = rows.Close()
			return err
		}
		var payload outboxPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			_ = rows.Close()
			return err
		}
		payloads[payload.Severity] = payload
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, payload := range payloads {
		payload.EndsAt = at
		if err := insertOutbox(ctx, tx, alertID, "resolved", payload, at); err != nil {
			return err
		}
	}
	return nil
}

func retireDeliveredSeverity(ctx context.Context, tx *sql.Tx, alertID int64, severity string, at time.Time) error {
	var body []byte
	err := tx.QueryRowContext(ctx, `
		SELECT payload
		FROM alert_outbox
		WHERE alert_id=$1 AND operation='firing' AND state='delivered'
		  AND payload->>'severity'=$2
		ORDER BY id DESC LIMIT 1
	`, alertID, severity).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		_, updateErr := tx.ExecContext(ctx, `
			UPDATE alert_outbox SET state='missed', last_error='severity changed before handoff'
			WHERE alert_id=$1 AND operation='firing' AND state='pending'
			  AND payload->>'severity'=$2
		`, alertID, severity)
		return updateErr
	}
	if err != nil {
		return err
	}
	var payload outboxPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	payload.EndsAt = at
	return insertOutbox(ctx, tx, alertID, "resolved", payload, at)
}

type lifecycleEvent struct {
	alertID   int64
	eventType string
	message   string
	metadata  map[string]any
	at        time.Time
}

type pendingLifecycleEvents []lifecycleEvent

func (events *pendingLifecycleEvents) insert(ctx context.Context, tx *sql.Tx, alertID int64, eventType, message string, metadata map[string]any, at time.Time) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	body, _ := json.Marshal(metadata)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO alert_event (alert_id, type, message, metadata, occurred_at)
		VALUES ($1,$2,$3,$4,$5)
	`, alertID, eventType, message, body, at)
	if err != nil {
		return fmt.Errorf("insert alert event: %w", err)
	}
	*events = append(*events, lifecycleEvent{
		alertID:   alertID,
		eventType: eventType,
		message:   message,
		metadata:  metadata,
		at:        at,
	})
	return nil
}

func (events pendingLifecycleEvents) log(ctx context.Context) {
	for _, event := range events {
		slog.InfoContext(
			ctx,
			"alert lifecycle event recorded",
			"alert_id", event.alertID,
			"event", event.eventType,
			"message", event.message,
			"occurred_at", event.at,
			"metadata", event.metadata,
		)
	}
}

func anomalyConditionKey(serviceID, indicatorID int, historical bool) string {
	prefix := "anomaly"
	if historical {
		prefix = "historical-anomaly"
	}
	return fmt.Sprintf("%s:%d:%d", prefix, serviceID, indicatorID)
}

func monitoringConditionKey(serviceID int, operation string) string {
	return fmt.Sprintf("monitoring:%d:%s", serviceID, operation)
}

func anomalyKind(historical bool) string {
	if historical {
		return KindHistoricalAnomaly
	}
	return KindAnomaly
}

func severityForCount(count, threshold int) string {
	if count >= threshold {
		return SeverityCritical
	}
	return SeverityWarning
}

func anomalyDescription(service string, count int, historical bool) string {
	if historical {
		return fmt.Sprintf("WeeWoo recovered metrics and detected %d anomalous time chunk(s) for %s. These observations occurred in the past and do not by themselves describe current health.", count, service)
	}
	return fmt.Sprintf("WeeWoo detected %d consecutive time chunk(s) whose latency distribution differed significantly from the learned reference for %s.", count, service)
}

func collectionDescription(service string, count int, retryAt time.Time) string {
	description := fmt.Sprintf("WeeWoo could not collect Prometheus metrics for %s. %d consecutive attempt(s) have failed.", service, count)
	if !retryAt.IsZero() {
		description += fmt.Sprintf(" The next attempt is scheduled for %s.", retryAt.UTC().Format(time.RFC3339))
	}
	return description
}

func resolutionMessage(reason string) string {
	switch reason {
	case "good_chunk":
		return "A Good chunk ended the anomaly condition"
	case "collection_recovered":
		return "Prometheus metrics collection recovered"
	case "accepted_as_normal":
		return "All anomalous occurrences were accepted as normal"
	case "monitoring_paused":
		return "Monitoring was paused"
	case "service_removed":
		return "The service was removed from monitoring"
	case "monitoring_interrupted":
		return "Monitoring was interrupted"
	default:
		return strings.ReplaceAll(reason, "_", " ")
	}
}

var (
	secretPattern         = regexp.MustCompile(`(?i)(token|password|secret|authorization)=([^\s&]+)`)
	authorizationPattern  = regexp.MustCompile(`(?i)(authorization:\s*)[^\r\n]+`)
	urlCredentialsPattern = regexp.MustCompile(`://([^/@\s]+)@`)
)

func sanitizeDetails(value string) string {
	value = strings.TrimSpace(value)
	value = secretPattern.ReplaceAllString(value, "$1=[redacted]")
	value = authorizationPattern.ReplaceAllString(value, "$1[redacted]")
	value = urlCredentialsPattern.ReplaceAllString(value, "://[redacted]@")
	if len(value) > 4000 {
		value = value[:4000]
	}
	return value
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeDetails(err.Error())
}

func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func (m *Manager) criticalThreshold(key string) int {
	value, err := m.cfg.GetConfig(key)
	if err != nil {
		return defaultCriticalConsecutive
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return defaultCriticalConsecutive
	}
	return parsed
}

func (m *Manager) configuredDuration(key string, fallback time.Duration) time.Duration {
	value, err := m.cfg.GetConfig(key)
	if err != nil || value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
