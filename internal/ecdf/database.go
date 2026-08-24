// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

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
func (c *database) WriteChunk(serviceID, indicatorID int, generation int64, timestamp time.Time, chunk []byte) error {
	timestamp = timestamp.Truncate(time.Second)
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("begin chunk write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.Exec(`
		INSERT INTO time_chunk (service_id, indicator_id, "timestamp", chunk, generation)
		VALUES ($2, $3, $4, $1, $5)
		ON CONFLICT (service_id, indicator_id, "timestamp")
		DO UPDATE SET chunk=EXCLUDED.chunk, collected_at=CURRENT_TIMESTAMP,
		              generation=EXCLUDED.generation
		WHERE time_chunk.generation <= EXCLUDED.generation
	`, chunk, serviceID, indicatorID, timestamp, generation)
	if err != nil {
		return fmt.Errorf("failed to write chunk: %w", err)
	}
	written, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect chunk write: %w", err)
	}
	if written == 0 {
		return tx.Commit()
	}
	_, err = tx.Exec(`
		INSERT INTO verdict (service_id, indicator_id, "timestamp", automated_good, pvalue, analysis_state, generation)
		VALUES ($1, $2, $3, NULL, NULL, 'pending', $4)
		ON CONFLICT (service_id, indicator_id, "timestamp")
		DO UPDATE SET automated_good=NULL, pvalue=NULL, analysis_state='pending',
		              review_override=NULL, reviewed_at=NULL, review_reason=NULL,
		              generation=EXCLUDED.generation
		WHERE verdict.generation < EXCLUDED.generation
	`, serviceID, indicatorID, timestamp, generation)
	if err != nil {
		return fmt.Errorf("failed to write chunk: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chunk write: %w", err)
	}
	return nil
}

// ReadChunk reads a time chunk from the database.
func (c *database) ReadChunk(serviceId int, indicatorId int, timestamp time.Time) ([]byte, error) {
	timestamp = timestamp.Truncate(time.Second)
	var chunk []byte
	err := c.db.QueryRow(`
		SELECT chunk
		FROM time_chunk
		WHERE service_id = $1
		  AND indicator_id = $2
		  AND "timestamp" = $3
  	`, serviceId, indicatorId, timestamp).Scan(&chunk)
	if err != nil {
		return nil, fmt.Errorf("failed to read chunk: %w", err)
	}
	return chunk, nil
}

// WriteVerdict records the latest verdict for a time chunk. The upsert permits
// a later review workflow to reverse a verdict without changing this API.
func (c *database) WriteVerdict(ctx context.Context, serviceID, indicatorID int, generation int64, timestamp time.Time, good bool, pValue float64) error {
	timestamp = timestamp.Truncate(time.Second)
	_, err := c.db.ExecContext(ctx, `
		UPDATE verdict
		SET automated_good=$5, pvalue=$6, analysis_state=CASE WHEN $5 THEN 'good' ELSE 'bad' END
		WHERE service_id=$1 AND indicator_id=$2 AND generation=$3
		  AND "timestamp"=$4
	`, serviceID, indicatorID, generation, timestamp, good, pValue)
	if err != nil {
		return fmt.Errorf("failed to write verdict: %w", err)
	}
	return nil
}

func (c *database) CountEligibleChunks(ctx context.Context, serviceID, indicatorID int, generation int64) (int, error) {
	var count int
	err := c.db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM verdict
		WHERE service_id=$1 AND indicator_id=$2 AND generation=$3
		  AND (
			analysis_state IN ('baseline', 'good')
			OR (analysis_state='bad' AND review_override=true)
		  )
	`, serviceID, indicatorID, generation).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count eligible chunks: %w", err)
	}
	return count, nil
}

// ScanGoodChunks finds all chunks from "good" samples in a given time range.
func (c *database) ScanGoodChunks(ctx context.Context, serviceID, indicatorID int, generation int64, out chan<- []byte) error {
	rows, err := c.db.QueryContext(ctx, `
		SELECT tc.chunk
		FROM time_chunk AS tc
		WHERE tc.service_id = $1
		  AND tc.indicator_id = $2
		  AND tc.generation = $3
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
		ORDER BY tc."timestamp"`, serviceID, indicatorID, generation)
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
