package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	databaseutil "github.com/uncertaintea-io/weewoo/internal/database"
)

type database struct {
	db     *sql.DB
	sqlite bool
}

// These keys must match the collection indicator IDs. Taking every publication
// lock prevents a material edit from racing an in-flight ECDF publish.
const (
	loadLatencyBaselineIndicatorID = 1
	timeOfDayBaselineIndicatorID   = 2
)

var baselinePublicationIndicatorIDs = []int{loadLatencyBaselineIndicatorID, timeOfDayBaselineIndicatorID}

// gets the value for a given key from the config table and returns the value. If the key is not found it returns an error, if the key is empty it returns an error.
func (c *database) GetConfig(key string) (string, error) {
	if key == "" {
		return "", errors.New("key is empty, please enter a valid key")
	}
	// using a known key its get the value for that key
	var value string
	rows, err := c.db.Query("SELECT value FROM config WHERE key = $1", key)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	//scans the rows for the value and returns the value
	for rows.Next() {
		err = rows.Scan(&value)
		if err != nil {
			return "", err
		}
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return value, nil
}

// sets the 'key' and 'value' strings into the config table.
func (c *database) SetConfig(key string, value string) error {
	return c.SetConfigs(map[string]string{key: value})
}

// SetConfigs atomically inserts or updates a group of configuration values.
func (c *database) SetConfigs(values map[string]string) error {
	if len(values) == 0 {
		return errors.New("at least one config value is required")
	}
	for key, value := range values {
		if key == "" || value == "" {
			return errors.New("key and value are required")
		}
	}
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("begin config update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for key, value := range values {
		if _, err := tx.Exec(`
			INSERT INTO config (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
		`, key, value); err != nil {
			return fmt.Errorf("set config %q: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit config update: %w", err)
	}
	return nil
}

// opens connection to the database for the functions below to use
func NewDatabaseConfig(db *sql.DB) Config {
	return &database{db: db, sqlite: databaseutil.IsSQLite(db)}
}

func (c *database) ReadDataSource(id int) (*DataSource, error) {
	var dataSource DataSource
	var pollingIntervalSeconds int64
	var urlString string
	//if the id is less than or equal to 0 it returns a error.
	if id <= 0 {
		return nil, fmt.Errorf("id must be greater than 0")
	}
	//if the id is a valid id and in the database it returns the data source.
	err := c.db.QueryRow("SELECT id, type, url, polling_interval FROM data_source WHERE id = $1", id).Scan(&dataSource.Id, &dataSource.DataType, &urlString, &pollingIntervalSeconds)
	if err != nil {
		return nil, err
	}
	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL %q: %w", urlString, err)
	}
	dataSource.URL = *parsedURL
	dataSource.PollingInterval = time.Duration(pollingIntervalSeconds) * time.Second
	return &dataSource, nil
}

func (c *database) WriteDataSource(ds *DataSource) error {
	pollingIntervalSeconds := int64(ds.PollingInterval / time.Second)
	if ds.Id == 0 {
		err := c.db.QueryRow(`
			INSERT INTO data_source (type, url, polling_interval)
			VALUES ($1, $2, $3)
			RETURNING id
		`, ds.DataType, ds.URL.String(), pollingIntervalSeconds).Scan(&ds.Id)
		if err != nil {
			return fmt.Errorf("failed to insert data source: %w", err)
		}
	} else {
		_, err := c.db.Exec("UPDATE data_source SET type = $1, url = $2, polling_interval = $3 WHERE id = $4", ds.DataType, ds.URL.String(), pollingIntervalSeconds, ds.Id)
		if err != nil {
			return fmt.Errorf("failed to update data source: %w", err)
		}
	}
	return nil
}

// writes a service to the database. if the id is 0 it inserts a new service, otherwise it updates the existing service.
func (c *database) WriteService(service *Service) error {
	intervalSeconds := int64(service.Interval / time.Second)
	// makes sure the id is not inserted at 0. if it is 0, it inserts a new service at a non zero id
	if service.Id == 0 {
		err := c.db.QueryRow(`
			INSERT INTO service (name, prometheus_url, load_query, latency_query, interval_seconds, paused)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, revision, generation
		`, service.Name, service.PrometheusURL, service.LoadQuery, service.LatencyQuery, intervalSeconds, service.Paused).Scan(&service.Id, &service.Revision, &service.Generation)
		if err != nil {
			return fmt.Errorf("failed to insert service: %w", err)
		}
	} else {
		return c.UpdateService(context.Background(), service, service.Revision, "system")
	}
	return nil
}

func (c *database) UpdateService(ctx context.Context, service *Service, expectedRevision int64, changedBy string) error {
	return c.updateService(ctx, service, expectedRevision, changedBy, false)
}

func (c *database) updateService(ctx context.Context, service *Service, expectedRevision int64, changedBy string, forceBaselineReset bool) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin service update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var previous Service
	var intervalSeconds int64
	var baselineResetAt sql.NullTime
	lockClause := " FOR UPDATE"
	if c.sqlite {
		lockClause = ""
	}
	err = tx.QueryRowContext(ctx, `
		SELECT id, name, prometheus_url, load_query, latency_query, interval_seconds,
		       paused, revision, generation, baseline_reset_at
		FROM service WHERE id=$1`+lockClause,
		service.Id).Scan(&previous.Id, &previous.Name, &previous.PrometheusURL, &previous.LoadQuery,
		&previous.LatencyQuery, &intervalSeconds, &previous.Paused, &previous.Revision,
		&previous.Generation, &baselineResetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrServiceNotFound
	}
	if err != nil {
		return fmt.Errorf("read service for update: %w", err)
	}
	previous.Interval = time.Duration(intervalSeconds) * time.Second
	if baselineResetAt.Valid {
		previous.BaselineResetAt = baselineResetAt.Time
	}
	if previous.Revision != expectedRevision {
		return ErrServiceConflict
	}
	material := forceBaselineReset || previous.PrometheusURL != service.PrometheusURL ||
		previous.LoadQuery != service.LoadQuery || previous.LatencyQuery != service.LatencyQuery ||
		previous.Interval != service.Interval
	service.Revision = previous.Revision + 1
	service.Generation = previous.Generation
	service.BaselineResetAt = previous.BaselineResetAt
	if material {
		service.Generation++
		service.BaselineResetAt = time.Now().UTC()
	}
	previousJSON, _ := json.Marshal(previous)
	currentJSON, _ := json.Marshal(service)
	if _, err := tx.ExecContext(ctx, `
		UPDATE service SET name=$1, prometheus_url=$2, load_query=$3, latency_query=$4,
		    interval_seconds=$5, paused=$6, revision=$7, generation=$8, baseline_reset_at=$9
		WHERE id=$10
	`, service.Name, service.PrometheusURL, service.LoadQuery, service.LatencyQuery,
		int64(service.Interval/time.Second), service.Paused, service.Revision, service.Generation,
		nullTime(service.BaselineResetAt), service.Id); err != nil {
		return fmt.Errorf("update service: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO service_revision
		    (service_id, previous_revision, new_revision, changed_by, material,
		     previous_configuration, new_configuration)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, service.Id, previous.Revision, service.Revision, changedBy, material, previousJSON, currentJSON); err != nil {
		return fmt.Errorf("insert service revision: %w", err)
	}
	if material {
		if !c.sqlite {
			for _, indicatorID := range baselinePublicationIndicatorIDs {
				if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, service.Id, indicatorID); err != nil {
					return fmt.Errorf("lock service baseline indicator %d: %w", indicatorID, err)
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ecdf WHERE service_id=$1`, service.Id); err != nil {
			return fmt.Errorf("invalidate service baseline: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit service update: %w", err)
	}
	return nil
}

// ResetServiceBaseline starts a new performance generation without changing
// the service's Prometheus configuration.
func (c *database) ResetServiceBaseline(ctx context.Context, id int, expectedRevision int64, changedBy string) (*Service, error) {
	service, err := c.ReadService(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrServiceNotFound
		}
		return nil, err
	}
	if err := c.updateService(ctx, service, expectedRevision, changedBy, true); err != nil {
		return nil, err
	}
	return service, nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func (c *database) ReadServiceHistory(id int) ([]ServiceChange, error) {
	rows, err := c.db.Query(`
		SELECT previous_revision, new_revision, changed_at, changed_by, material,
		       previous_configuration, new_configuration
		FROM service_revision WHERE service_id=$1 ORDER BY new_revision DESC
	`, id)
	if err != nil {
		return nil, fmt.Errorf("read service history: %w", err)
	}
	defer rows.Close()
	history := make([]ServiceChange, 0)
	for rows.Next() {
		var change ServiceChange
		var previousJSON, currentJSON []byte
		change.ServiceID = id
		if err := rows.Scan(&change.PreviousRevision, &change.NewRevision, &change.ChangedAt,
			&change.ChangedBy, &change.Material, &previousJSON, &currentJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(previousJSON, &change.Previous); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(currentJSON, &change.Current); err != nil {
			return nil, err
		}
		history = append(history, change)
	}
	return history, rows.Err()
}

// reads a service from the database.
func (c *database) ReadService(id int) (*Service, error) {
	var service Service
	var intervalSeconds int64
	if id <= 0 {
		return nil, fmt.Errorf("id must be greater than 0")
	}
	var baselineResetAt sql.NullTime
	err := c.db.QueryRow("SELECT id, name, prometheus_url, load_query, latency_query, interval_seconds, paused, revision, generation, baseline_reset_at FROM service WHERE id = $1", id).Scan(&service.Id, &service.Name, &service.PrometheusURL, &service.LoadQuery, &service.LatencyQuery, &intervalSeconds, &service.Paused, &service.Revision, &service.Generation, &baselineResetAt)
	if err != nil {
		return nil, fmt.Errorf("failed to read service: %w", err)
	}
	service.Interval = time.Duration(intervalSeconds) * time.Second
	if baselineResetAt.Valid {
		service.BaselineResetAt = baselineResetAt.Time
	}
	parsedURL, err := url.Parse(service.PrometheusURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL string to url.URL: %w", err)
	}
	service.PrometheusURL = parsedURL.String()
	return &service, nil
}

// this function reads all the services from the database and returns them as a slice of service objects
func (c *database) ReadAllServices() ([]*Service, error) {
	rows, err := c.db.Query("SELECT id, name, prometheus_url, load_query, latency_query, interval_seconds, paused, revision, generation, baseline_reset_at FROM service")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	services := make([]*Service, 0)
	for rows.Next() {
		service, err := c.RowsToObject(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return services, nil
}

func (c *database) DeleteService(id int) error {
	if id <= 0 {
		return fmt.Errorf("id must be greater than 0")
	}
	result, err := c.db.Exec("DELETE FROM service WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to inspect deleted service: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// this function converts a row from the database to a service object
func (c *database) RowsToObject(rows *sql.Rows) (*Service, error) {
	var service Service
	var intervalSeconds int64
	var baselineResetAt sql.NullTime
	err := rows.Scan(&service.Id, &service.Name, &service.PrometheusURL, &service.LoadQuery, &service.LatencyQuery, &intervalSeconds, &service.Paused, &service.Revision, &service.Generation, &baselineResetAt)
	if err != nil {
		return nil, err
	}
	service.Interval = time.Duration(intervalSeconds) * time.Second
	if baselineResetAt.Valid {
		service.BaselineResetAt = baselineResetAt.Time
	}
	return &service, nil
}

func (c *database) Close() {
	c.db.Close()
}
