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
	_, err := c.db.Exec(`
			WITH updated AS (
				UPDATE time_chunk
				SET chunk = $1
				WHERE service_id = $2 AND indicator_id = $3 AND "timestamp" = $4
				RETURNING chunk
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
	err := c.db.QueryRow("SELECT chunk FROM time_chunk WHERE service_id = $1 AND indicator_id = $2 AND \"timestamp\" = $3", serviceId, indicatorId, timestamp).Scan(&chunk)
	if err != nil {
		return nil, fmt.Errorf("failed to read chunk: %w", err)
	}
	return chunk, nil
}

// ScanGoodChunks finds all chunks from "good" samples in a given time range.
func (c *database) ScanGoodChunks(ctx context.Context, serviceId int, indicatorId int, start, end time.Time, out chan<- []byte) error {
	// TODO: filter by good chunks
	rows, err := c.db.QueryContext(ctx, "SELECT chunk FROM time_chunk WHERE service_id = $1 AND indicator_id = $2 AND \"timestamp\" BETWEEN $3 AND $4", serviceId, indicatorId, start, end)
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
