package ecdf

import (
	"database/sql"
	"time"
)

type database struct {
	db *sql.DB
}

func NewDatabaseChunkStore(db *sql.DB) ChunkStore {
	return &database{db: db}
}

// WriteChunk writes a time chunk to the database.
func (c *database) WriteChunk(service_id int, indicator_id int, timestamp time.Time, x, y []Sample) error {
	chunk, err := Encode(timestamp, x, y)
	if err != nil {
		return err
	}
	_, err = c.db.Exec("INSERT INTO time_chunk (service_id, indicator_id, \"Timestamp\", chunk) VALUES ($1, $2, $3, $4)", service_id, indicator_id, timestamp, chunk)
	if err != nil {
		return err
	}
	return nil
}

// ReadChunk reads a time chunk from the database.
func (c *database) ReadChunk(service_id int, indicator_id int, timestamp time.Time) (TimeChunk, error) {
	var chunk []byte
	err := c.db.QueryRow("SELECT chunk FROM time_chunk WHERE service_id = $1 AND indicator_id = $2 AND \"Timestamp\" = $3", service_id, indicator_id, timestamp).Scan(&chunk)
	if err != nil {
		return TimeChunk{}, err
	}
	timestamp, x, y, err := Decode(chunk)
	if err != nil {
		return TimeChunk{}, err
	}
	return TimeChunk{Timestamp: timestamp, X: x, Y: y}, nil
}

// ScanGoodChunks scans by using readchunks and filtering for good chunks in the database.
// TODO: not needed yet because we don't have a good way to filter good chunks yet.
// func (c *database) ScanGoodChunks(serviceId int, indicatorId int, start, end time.Time, out chan<- TimeChunk) error {
// 	rows, err := c.db.Query("SELECT \"Timestamp\", chunk FROM time_chunk WHERE service_id = $1 AND indicator_id = $2 AND \"Timestamp\" BETWEEN $3 AND $4", serviceId, indicatorId, start, end)
// 	if err != nil {
// 		return err
// 	}
// 	defer rows.Close()
// 	for rows.Next() {
// 		var timestamp time.Time
// 		var chunk []byte
// 		err = rows.Scan(&timestamp, &chunk)
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
