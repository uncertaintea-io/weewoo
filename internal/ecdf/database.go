package ecdf

import (
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
func (c *database) WriteChunk(serviceId int, indicatorId int, timestamp time.Time, x, y []Sample) error {
	chunk, err := Encode(timestamp, x, y)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(`
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
func (c *database) ReadChunk(serviceId int, indicatorId int, timestamp time.Time) (TimeChunk, error) {
	var chunk []byte
	err := c.db.QueryRow("SELECT chunk FROM time_chunk WHERE service_id = $1 AND indicator_id = $2 AND \"timestamp\" = $3", serviceId, indicatorId, timestamp).Scan(&chunk)
	if err != nil {
		return TimeChunk{}, fmt.Errorf("failed to read chunk: %w", err)
	}
	timestamp, x, y, err := Decode(chunk)
	if err != nil {
		return TimeChunk{}, fmt.Errorf("failed to decode chunk: %w", err)
	}
	return TimeChunk{Timestamp: timestamp, X: x, Y: y}, nil
}

// ScanGoodChunks scans by using readchunks and filtering for good chunks in the database.
// TODO: not needed yet because we don't have a good way to filter good chunks yet.
// func (c *database) ScanGoodChunks(serviceId int, indicatorId int, start, end time.Time, out chan<- TimeChunk) error {
// 	rows, err := c.db.Query("SELECT chunk FROM time_chunk WHERE service_id = $1 AND indicator_id = $2 AND \"timestamp\" BETWEEN $3 AND $4", serviceId, indicatorId, start, end)
// 	if err != nil {
// 		return err
// 	}
// 	defer rows.Close()
// 	for rows.Next() {
// 		var chunk []byte
// 		err = rows.Scan(&chunk)
// 		if err != nil {
// 			return err
// 		}
// 		timestamp, x, y, err := Decode(chunk)
// 		if err != nil {
// 			return err
// 		}
// 		out <- TimeChunk{Timestamp: timestamp, X: x, Y: y}
// 	}
// 	return nil
// }
