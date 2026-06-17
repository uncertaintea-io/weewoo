package main

import (
	"log"
	_"github.com/jackc/pgx/v5/stdlib"
	"errors"
)

// gets the value for a given key from the config table and returns the value. If the key is not found it returns an error, if the key is empty it returns an error.
func (c *database) getConfig(key string) string {
	if key == "" {
		return errors.New("key is empty, please enter a valid key").Error()
	}
	// using a known key its get the value for that key
	var value string
	rows, err := c.db.Query("SELECT value FROM config WHERE key = $1", key)
	if err != nil {
		log.Fatal(err)
	}
	//scans the rows for the value and returns the value
	for rows.Next() {
		err = rows.Scan(&value)
		if err != nil {
			log.Fatal(err)
		}
	}
	defer rows.Close()
	return value
}

// sets the 'key' and 'value' strings into the config table and returns a boolean indicating success or failure.
func (c *database) setConfig(key string, value string) bool {
	if key == "" || value == "" {
		return false
	}
	//inserts the 'key' and 'value' strings into the config table
	_, err := c.db.Exec("INSERT INTO config (key, value) VALUES ($1, $2)", key, value)
	if err != nil {
		return false
	}
	return true
}

func (c *database) Close() {
	c.db.Close()
}
