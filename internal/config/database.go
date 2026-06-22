package config

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type database struct {
	db *sql.DB
}

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
	if key == "" || value == "" {
		return errors.New("key and value are required")
	}
	_, err := c.db.Exec(`
		WITH updated AS (
			UPDATE config
			SET value = $2
			WHERE key = $1
			RETURNING key
		)
		INSERT INTO config (key, value)
		SELECT $1, $2
		WHERE NOT EXISTS (SELECT 1 FROM updated)
	`, key, value)
	if err != nil {
		return fmt.Errorf("failed to set config: %w", err)
	}
	return nil
}

// opens connection to the database for the functions below to use
func NewDatabaseConfig(conn string) (Config, error) {
	connection, err := sql.Open("pgx", conn)
	if err != nil {
		return nil, err
	}
	return &database{db: connection}, nil
}

func (c *database) ReadData(id int) (*DataSource, error) {
	var dataSource DataSource
	var pollingIntervalSeconds int
	err := c.db.QueryRow("SELECT id, data_type, url, polling_interval FROM data_source WHERE id = $1", id).Scan(&dataSource.id, &dataSource.data_type, &dataSource.url, &pollingIntervalSeconds)
	if err != nil {
		return nil, err
	}
	dataSource.polling_interval = time.Duration(pollingIntervalSeconds) * time.Second
	return &dataSource, nil
}

func (c *database) WriteData(ds *DataSource) (int, error) {
	pollingIntervalSeconds := int(ds.polling_interval / time.Second)
	if ds.id == 0 {
		err := c.db.QueryRow(`
			INSERT INTO data_source (id, data_type, url, polling_interval)
			VALUES ((SELECT COALESCE(MAX(id), 0) + 1 FROM data_source), $1, $2, $3)
			RETURNING id
		`, ds.data_type, ds.url, pollingIntervalSeconds).Scan(&ds.id)
		if err != nil {
			return 0, fmt.Errorf("failed to insert data source: %w", err)
		}
		return ds.id, nil
	} else {
		err := c.db.QueryRow("UPDATE data_source SET data_type = $1, url = $2, polling_interval = $3 WHERE id = $4 RETURNING id", ds.data_type, ds.url, pollingIntervalSeconds, ds.id).Scan(&ds.id)
		if err != nil {
			return 0, fmt.Errorf("failed to update data source: %w", err)
		}
		return ds.id, nil
	}
}

func (c *database) Close() {
	c.db.Close()
}
