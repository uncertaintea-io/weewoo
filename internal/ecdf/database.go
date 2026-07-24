package ecdf

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type database struct {
	db *sql.DB
}

func NewDatabaseChunkStore(db *sql.DB) ChunkStore {
	return &database{db: db}
}

// WriteChunk writes a time chunk to the database.
func (c *database) WriteChunk(serviceId int, indicatorId int, timestamp time.Time, chunk []byte) error {
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("begin chunk write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`
			WITH updated AS (
				UPDATE time_chunk
				SET chunk = $1, collected_at = CURRENT_TIMESTAMP
				WHERE service_id = $2
				  AND indicator_id = $3
				  AND "timestamp" = $4::timestamptz(0)
				RETURNING 1
			)
			INSERT INTO time_chunk (service_id, indicator_id, "timestamp", chunk)
			SELECT $2, $3, $4, $1
			WHERE NOT EXISTS (SELECT 1 FROM updated)
	`, chunk, serviceId, indicatorId, timestamp)
	if err != nil {
		return fmt.Errorf("failed to write chunk: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO verdict (service_id, indicator_id, "timestamp", automated_good, pvalue, analysis_state)
		VALUES ($1, $2, $3::timestamptz(0), NULL, NULL, 'pending')
		ON CONFLICT (service_id, indicator_id, "timestamp") DO NOTHING
	`, serviceId, indicatorId, timestamp); err != nil {
		return fmt.Errorf("failed to initialize pending verdict: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chunk write: %w", err)
	}
	return nil
}

// ReadChunk reads a time chunk from the database.
func (c *database) ReadChunk(serviceId int, indicatorId int, timestamp time.Time) ([]byte, error) {
	var chunk []byte
	err := c.db.QueryRow(`
		SELECT chunk
		FROM time_chunk
		WHERE service_id = $1
		  AND indicator_id = $2
		  AND "timestamp" = $3::timestamptz(0)
  	`, serviceId, indicatorId, timestamp).Scan(&chunk)
	if err != nil {
		return nil, fmt.Errorf("failed to read chunk: %w", err)
	}
	return chunk, nil
}

// WriteVerdict records the latest verdict for a time chunk. The upsert permits
// a later review workflow to reverse a verdict without changing this API.
func (c *database) WriteVerdict(ctx context.Context, serviceID, indicatorID int, timestamp time.Time, good bool, pValue float64) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO verdict (service_id, indicator_id, "timestamp", automated_good, pvalue, analysis_state)
		VALUES ($1, $2, $3::timestamptz(0), $4, $5, CASE WHEN $4 THEN 'good' ELSE 'bad' END)
		ON CONFLICT (service_id, indicator_id, "timestamp")
		DO UPDATE SET automated_good = EXCLUDED.automated_good,
		              pvalue = EXCLUDED.pvalue,
		              analysis_state = EXCLUDED.analysis_state
	`, serviceID, indicatorID, timestamp, good, pValue)
	if err != nil {
		return fmt.Errorf("failed to write verdict: %w", err)
	}
	return nil
}

func (c *database) CountEligibleChunks(ctx context.Context, serviceID, indicatorID int) (int, error) {
	var count int
	err := c.db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM verdict
		WHERE service_id=$1 AND indicator_id=$2
		  AND (
			analysis_state IN ('baseline', 'good')
			OR (analysis_state='bad' AND review_override=true)
		  )
	`, serviceID, indicatorID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count eligible chunks: %w", err)
	}
	return count, nil
}

func (c *database) HasPendingRecovery(ctx context.Context, serviceID int) (bool, error) {
	var pending bool
	err := c.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM collection_backlog
			WHERE service_id=$1 AND state IN ('pending', 'collecting')
		)
	`, serviceID).Scan(&pending)
	if err != nil {
		return false, fmt.Errorf("check collection recovery backlog: %w", err)
	}
	return pending, nil
}

// ScanGoodChunks finds all chunks from "good" samples in a given time range.
func (c *database) ScanGoodChunks(ctx context.Context, serviceId int, indicatorId int, out chan<- []byte) error {
	rows, err := c.db.QueryContext(ctx, `
		SELECT tc.chunk
		FROM time_chunk AS tc
		WHERE tc.service_id = $1
		  AND tc.indicator_id = $2
		  AND EXISTS (
			SELECT 1
			FROM verdict AS v
			WHERE v.service_id = tc.service_id
			  AND v.indicator_id = tc.indicator_id
			  AND v."timestamp" = tc."timestamp"
			  AND (
				v.analysis_state IN ('baseline', 'good')
				OR (v.analysis_state='bad' AND v.review_override=true)
			  )
		)
		ORDER BY tc."timestamp"`, serviceId, indicatorId)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var chunk []byte
		err = rows.Scan(&chunk)
		if err != nil {
			return err
		}
		select {
		case out <- chunk:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}
