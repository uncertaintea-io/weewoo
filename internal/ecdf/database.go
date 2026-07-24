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

func NewDatabaseVerdictStore(db *sql.DB) VerdictStore {
	return &database{db: db}
}

// WriteChunk writes a time chunk to the database.
func (c *database) WriteChunk(serviceId int, indicatorId int, timestamp time.Time, chunk []byte) error {
	_, err := c.db.Exec(`
			WITH updated AS (
				UPDATE time_chunk
				SET chunk = $1
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
		INSERT INTO verdict (service_id, indicator_id, "timestamp", good, pvalue)
		VALUES ($1, $2, $3::timestamptz(0), $4, $5)
		ON CONFLICT (service_id, indicator_id, "timestamp")
		DO UPDATE SET good = EXCLUDED.good, pvalue = EXCLUDED.pvalue
	`, serviceID, indicatorID, timestamp, good, pValue)
	if err != nil {
		return fmt.Errorf("failed to write verdict: %w", err)
	}
	return nil
}

// ScanGoodChunks finds all chunks from "good" samples in a given time range.
func (c *database) ScanGoodChunks(ctx context.Context, serviceId int, indicatorId int, out chan<- []byte) error {
	rows, err := c.db.QueryContext(ctx, `
		SELECT tc.chunk
		FROM time_chunk AS tc
		WHERE tc.service_id = $1
		  AND tc.indicator_id = $2
		AND NOT EXISTS (
			SELECT 1
			FROM verdict AS v
			WHERE v.service_id = tc.service_id
			  AND v.indicator_id = tc.indicator_id
			  AND v."timestamp" = tc."timestamp"
			  AND v.good = false
	)`, serviceId, indicatorId)
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
