package config

import (
	"database/sql"
	"errors"
	"fmt"
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

func (c *database) Close() {
	c.db.Close()
}

// opens connection to the database for the functions below to use
func NewDatabaseConfig(conn string) (Config, error) {
	connection, err := sql.Open("pgx", conn)
	if err != nil {
		return nil, err
	}
	return &database{db: connection}, nil
}
