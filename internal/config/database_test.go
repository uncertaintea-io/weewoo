package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// this tests that the connect function returns a non-nil value.
func TestNewDatabaseConfig(t *testing.T) {
	conn := os.Getenv("DATABASE_URL")
	if conn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	config, err := NewDatabaseConfig(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer config.Close()
	assert.NotNil(t, config)
}

func newDatabaseConfig(t *testing.T) Config {
	conn := os.Getenv("DATABASE_URL")
	if conn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	config, err := NewDatabaseConfig(conn)
	require.NoError(t, err)
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
}
