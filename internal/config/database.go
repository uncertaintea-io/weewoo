package main

import (
	"database/sql"
	"log"
)

type database struct{
	db *sql.DB
}

type Config interface {
	getConfig(key string) string
	setConfig(key string, value string) bool
	Close()
}

// opens connection to the database for the functions below to use
func connect() Config {
	connection, err := sql.Open("pgx", "postgres://brippy:Shrub123@tau:5432/brippy?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	return &database{db: connection}
}
