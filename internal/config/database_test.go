package config

import (
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// this tests that the connect function returns a non-nil value.
func TestNewDatabaseConfig(t *testing.T) {
	newDatabaseConfig(t)
}

func newDatabaseConfig(t *testing.T) Config {
	conn := os.Getenv("DATABASE_URL")
	if conn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", conn)
	if err != nil {
		t.Fatal(err)
	}
	config := NewDatabaseConfig(db, "http://localhost:9093")
	require.NotNil(t, config)
	return config
}

// this test test the get/set config functions along side the read/write data source functions.
func TestGetConfigDatabase(t *testing.T) {
	//t.Skip() // comment this out to run the test manually
	config := newDatabaseConfig(t)
	defer config.Close()
	testConfigFunctions(t, config)
	testDataSourceFunctions(t, config)
	testServiceFunctions(t, config)
}
