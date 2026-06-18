package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// this tests that the connect function returns a non-nil value.
func TestNewDatabaseConfig(t *testing.T) {
	config, err := NewDatabaseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer config.Close()
	assert.NotNil(t, config)
}

func newDatabaseConfig(t *testing.T) Config {
	config, err := NewDatabaseConfig(os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	require.NotNil(t, config)
	return config
}

func TestGetConfigDatabase(t *testing.T) {
	//t.Skip() // comment this out to run the test manually
	config := newDatabaseConfig(t)
	defer config.Close()
	testGetConfig(t, config)
}

func TestSetConfigDatabase(t *testing.T) {
	//t.Skip() // comment this out to run the test manually
	config := newDatabaseConfig(t)
	defer config.Close()
	testSetConfig(t, config)
}
