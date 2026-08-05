package alerting

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	KindAnomaly            = "anomaly"
	KindHistoricalAnomaly  = "historical_anomaly"
	KindCollectionFailure  = "collection_failure"
	KindMonitoringImpaired = "monitoring_impaired"

	StatusFiring   = "firing"
	StatusResolved = "resolved"

	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

var ErrReviewConflict = errors.New("alert occurrence review changed")
var ErrOccurrenceNotFound = errors.New("alert occurrence not found")
var ErrCDFNotApplicable = errors.New("CDF details are only available for anomaly occurrences")

// AnalysisOutcome is the complete durable result of analyzing one time chunk.
// Recording a live outcome updates the Verdict and matching alert condition
// atomically. A historical outcome updates only the Verdict.
type AnalysisOutcome struct {
	ServiceID        int
	ServiceName      string
	IndicatorID      int
	Indicator        string
	Timestamp        time.Time
	Load             float64
	PValue           float64
	Threshold        float64
	Anomalous        bool
	Historical       bool
	GeneratorURL     string
	Description      string
	TechnicalDetails string
}

type CollectionFailure struct {
	ServiceID    int
	ServiceName  string
	WindowStart  time.Time
	WindowEnd    time.Time
	Attempt      int
	RetryAt      time.Time
	Error        error
	GeneratorURL string
}

type MonitoringFailure struct {
	ServiceID        int
	ServiceName      string
	IndicatorID      int
	OccurredAt       time.Time
	Operation        string
	Description      string
	TechnicalDetails string
	GeneratorURL     string
}

type Alert struct {
	ID                int64        `json:"id"`
	ServiceID         *int         `json:"serviceId,omitempty"`
	ServiceName       string       `json:"serviceName"`
	IndicatorID       *int         `json:"indicatorId,omitempty"`
	Kind              string       `json:"kind"`
	Severity          string       `json:"severity"`
	Status            string       `json:"status"`
	Title             string       `json:"title"`
	Description       string       `json:"description"`
	Impact            string       `json:"impact"`
	SuggestedAction   string       `json:"suggestedAction"`
	TechnicalDetails  string       `json:"technicalDetails"`
	StartedAt         time.Time    `json:"startedAt"`
	LastOccurredAt    time.Time    `json:"lastOccurredAt"`
	ResolvedAt        *time.Time   `json:"resolvedAt,omitempty"`
	ResolutionReason  string       `json:"resolutionReason,omitempty"`
	OccurrenceCount   int          `json:"occurrenceCount"`
	ConsecutiveCount  int          `json:"consecutiveCount"`
	AlertmanagerState string       `json:"alertmanagerState"`
	AlertmanagerError string       `json:"alertmanagerError,omitempty"`
	Occurrences       []Occurrence `json:"occurrences"`
	Events            []Event      `json:"events"`
}

type Occurrence struct {
	ID               int64          `json:"id"`
	Kind             string         `json:"kind"`
	OccurredAt       time.Time      `json:"occurredAt"`
	DetectedAt       time.Time      `json:"detectedAt"`
	WindowStart      *time.Time     `json:"windowStart,omitempty"`
	WindowEnd        *time.Time     `json:"windowEnd,omitempty"`
	ChunkTimestamp   *time.Time     `json:"chunkTimestamp,omitempty"`
	Summary          string         `json:"summary"`
	TechnicalDetails string         `json:"technicalDetails"`
	Evidence         map[string]any `json:"evidence"`
	ReviewRevision   int64          `json:"reviewRevision"`
	ReviewOverride   *bool          `json:"reviewOverride,omitempty"`
	ReviewedAt       *time.Time     `json:"reviewedAt,omitempty"`
	ReviewReason     string         `json:"reviewReason,omitempty"`
}

type Event struct {
	Type       string         `json:"type"`
	Message    string         `json:"message"`
	Metadata   map[string]any `json:"metadata"`
	OccurredAt time.Time      `json:"occurredAt"`
}

type ReviewResult struct {
	OccurrenceID  int64     `json:"occurrenceId"`
	Revision      int64     `json:"revision"`
	Accepted      bool      `json:"accepted"`
	ReviewedAt    time.Time `json:"reviewedAt"`
	AlertResolved bool      `json:"alertResolved"`
}

// CDFDetails is the data collected for a future occurrence-level CDF plot.
// Values and counts are preserved exactly as returned by the service queries.
type CDFDetails struct {
	SchemaVersion  int         `json:"schemaVersion"`
	AlertID        int64       `json:"alertId"`
	OccurrenceID   int64       `json:"occurrenceId"`
	ServiceID      int         `json:"serviceId"`
	IndicatorID    int         `json:"indicatorId"`
	ChunkTimestamp time.Time   `json:"chunkTimestamp"`
	Load           []CDFSample `json:"load"`
	Latency        []CDFSample `json:"latency"`
	CDF            CDFStatus   `json:"cdf"`
}

type CDFSample struct {
	Value float64 `json:"value"`
	Count uint64  `json:"count"`
}

type CDFStatus struct {
	Status      string `json:"status"`
	Description string `json:"description"`
}

// Recorder is the small interface used by collection and analysis.
type Recorder interface {
	RecordAnalysis(context.Context, AnalysisOutcome) error
	RecordBaseline(context.Context, int, int, time.Time) error
	RecordAnalysisFailure(context.Context, AnalysisOutcome, error) error
	RecordCollectionFailure(context.Context, CollectionFailure) error
	ResolveCollection(context.Context, int, time.Time) error
	RecordMonitoringFailure(context.Context, MonitoringFailure) error
	ResolveMonitoring(context.Context, int, string, time.Time) error
	InterruptAnomalies(context.Context, int, time.Time) error
	CloseService(context.Context, int, string, time.Time) error
}

type AnalysisRecorder interface {
	RecordAnalysis(context.Context, AnalysisOutcome) error
	RecordBaseline(context.Context, int, int, time.Time) error
	RecordAnalysisFailure(context.Context, AnalysisOutcome, error) error
}

// TransactionalCollectionRecorder records collection failures in a
// caller-owned database transaction.
type TransactionalCollectionRecorder interface {
	// RecordCollectionFailureTx writes the failure using tx and returns a
	// callback that logs the resulting lifecycle events. The caller must invoke
	// the callback only after tx commits successfully and discard it otherwise.
	RecordCollectionFailureTx(context.Context, *sql.Tx, CollectionFailure) (func(), error)
}
