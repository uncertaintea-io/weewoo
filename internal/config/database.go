package config

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
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

func (c *database) ReadDataSource(id int) (*DataSource, error) {
	var dataSource DataSource
	var pollingIntervalSeconds int
	var urlString string
	//if the id is less than or equal to 0 it returns a error.
	if id <= 0 {
		return nil, fmt.Errorf("id must be greater than 0")
	}
	// //if the id is a valid id but not in the database it returns a error.
	// if err := c.db.QueryRow("SELECT Id FROM data_source WHERE Id = $1", id).Scan(&id); err != nil {
	// 	if err == sql.ErrNoRows {
	// 		return nil, fmt.Errorf("id not found in database")
	// 	}
	// 	return nil, err
	// }

	//if the id is a valid id and in the database it returns the data source.
	err := c.db.QueryRow("SELECT Id, DataType, URL, PollingInterval FROM data_source WHERE Id = $1", id).Scan(&dataSource.Id, &dataSource.DataType, &urlString, &pollingIntervalSeconds)
	if err != nil {
		return nil, err
	}
	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL string to url.URL: %w", err)
	}
	dataSource.URL = *parsedURL
	dataSource.PollingInterval = time.Duration(pollingIntervalSeconds) * time.Second
	return &dataSource, nil
}

func (c *database) WriteDataSource(ds *DataSource) (int, error) {
	pollingIntervalSeconds := int(ds.PollingInterval / time.Second)
	if ds.Id == 0 {
		err := c.db.QueryRow(`
			INSERT INTO data_source (Id, DataType, URL, PollingInterval)
			VALUES ((SELECT COALESCE(MAX(Id), 0) + 1 FROM data_source), $1, $2, $3)
			RETURNING Id
		`, ds.DataType, ds.URL.String(), pollingIntervalSeconds).Scan(&ds.Id)
		if err != nil {
			return 0, fmt.Errorf("failed to insert data source: %w", err)
		}
		return ds.Id, nil
	} else {
		err := c.db.QueryRow("UPDATE data_source SET DataType = $1, URL = $2, PollingInterval = $3 WHERE Id = $4 RETURNING Id", ds.DataType, ds.URL.String(), pollingIntervalSeconds, ds.Id).Scan(&ds.Id)
		if err != nil {
			return 0, fmt.Errorf("failed to update data source: %w", err)
		}
		return ds.Id, nil
	}
}

func (c *database) Close() {
	c.db.Close()
}
