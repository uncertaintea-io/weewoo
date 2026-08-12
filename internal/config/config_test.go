package config

import (
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// this is one testing function that tests both getConfig and setConfig making them deterministic. allowing set to be first and then get to be second.
func testConfigFunctions(t *testing.T, config Config) {
	// testing setConfig
	inputKey := []string{"key1", "key2", "key3", "key4", "key5", "key6", "key7", "key8"}
	inputValue := []string{"value1", "value2", "value3", "value4", "value5", "value6", "value7", "value8"}
	for i := range inputKey {
		t.Run("set_"+strconv.Itoa(i), func(t *testing.T) {
			err := config.SetConfig(inputKey[i], inputValue[i])
			assert.NoError(t, err)
		})
	}
	// testing getConfig
	for i, key := range inputKey {
		t.Run("get_"+strconv.Itoa(i), func(t *testing.T) {
			got, err := config.GetConfig(key)
			assert.NoError(t, err)
			assert.Equal(t, inputValue[i], got)
		})
	}
	// testing getConfig with missing key
	got, err := config.GetConfig("missing")
	assert.NoError(t, err)
	assert.Equal(t, "", got)
	// testing getConfig with empty key
	got, err = config.GetConfig("")
	assert.Equal(t, "", got)
	assert.EqualError(t, err, "key is empty, please enter a valid key")
	// testing setConfig with empty key
	err = config.SetConfig("", "value")
	assert.EqualError(t, err, "key and value are required")
	// testing setConfig with empty value
	err = config.SetConfig("key", "")
	assert.EqualError(t, err, "key and value are required")
	// grouped writes are available for settings that must change together
	err = config.SetConfigs(map[string]string{"grouped1": "value1", "grouped2": "value2"})
	require.NoError(t, err)
	grouped, err := config.GetConfig("grouped2")
	require.NoError(t, err)
	assert.Equal(t, "value2", grouped)
}

// this is one testing function that tests both readData and writeData making them deterministic.
func testDataSourceFunctions(t *testing.T, config Config) {
	var writtenID int
	u := url.URL{Scheme: "https", Host: "example.com"}

	// testing writeData on a counter for the id. also tests what happens when there is no record for the id that was passed in.
	t.Run("WriteData", func(t *testing.T) {
		ds := &DataSource{
			DataType:        "test",
			URL:             u,
			PollingInterval: 10 * time.Second,
		}

		err := config.WriteDataSource(ds)
		require.NoError(t, err)
		require.NotZero(t, ds.Id)
		writtenID = ds.Id
	})
	// testing readData.
	t.Run("ReadData", func(t *testing.T) {
		require.NotZero(t, writtenID)
		data, err := config.ReadDataSource(writtenID)
		assert.NoError(t, err)
		require.NotNil(t, data)
		assert.Equal(t, "test", data.DataType)
		assert.Equal(t, "https://example.com", data.URL.String())
		assert.Equal(t, 10*time.Second, data.PollingInterval)
		// testing when there is no record for the id that was passed in.
		t.Logf("testing when there is no record for the id that was passed in. id: %d", writtenID+10)
		data, err = config.ReadDataSource(writtenID + 10)
		t.Log("error: ", err)
		t.Log("data: ", data)
		assert.Error(t, err)
		assert.Nil(t, data)
		// testing when the id is 0 it returns a error.
		data, err = config.ReadDataSource(0)
		assert.Nil(t, data)
		assert.Error(t, err)

		// testing when the id is negative it returns an error.
		data, err = config.ReadDataSource(-1)
		assert.Error(t, err)
		assert.Nil(t, data)
	})
}

func testServiceFunctions(t *testing.T, config Config) {
	var writtenID int
	name := "test"

	// testing writeService on a counter for the id. also tests what happens when there is no record for the id that was passed in.
	t.Run("WriteService", func(t *testing.T) {
		service := &Service{
			Name:          name,
			PrometheusURL: "http://example.com",
			LoadQuery:     "load_query",
			LatencyQuery:  "latency_query",
			Interval:      10 * time.Second,
		}
		err := config.WriteService(service)
		require.NoError(t, err)
		require.NotZero(t, service.Id)
		writtenID = service.Id
	})
	// testing readService.
	t.Run("ReadService", func(t *testing.T) {
		data, err := config.ReadService(writtenID)
		assert.NoError(t, err)
		require.NotNil(t, data)
		assert.Equal(t, name, data.Name)
		assert.Equal(t, "http://example.com", data.PrometheusURL)
		assert.Equal(t, "load_query", data.LoadQuery)
		assert.Equal(t, "latency_query", data.LatencyQuery)
		assert.Equal(t, 10*time.Second, data.Interval)
		// testing when there is no record for the id that was passed in.
		t.Logf("testing when there is no record for the id that was passed in. id: %d", writtenID+10)
		data, err = config.ReadService(writtenID + 10)
		t.Log("error: ", err)
		t.Log("data: ", data)
		assert.Error(t, err)
		assert.Nil(t, data)
		// testing when the id is 0 it returns a error.
		data, err = config.ReadService(0)
		assert.Nil(t, data)
		assert.Error(t, err)

		// testing when the id is negative it returns an error.
		data, err = config.ReadService(-1)
		assert.Error(t, err)
		assert.Nil(t, data)
	})
	t.Run("DeleteService", func(t *testing.T) {
		require.NoError(t, config.DeleteService(writtenID))
		deleted, err := config.ReadService(writtenID)
		assert.Error(t, err)
		assert.Nil(t, deleted)
	})
}

func TestReadingYamlFile(t *testing.T) {
	systemSettings, err := ReadSystemSettings("config.yaml")
	assert.NoError(t, err)
	assert.NotNil(t, systemSettings)
	assert.Equal(t, "postgresql", systemSettings.Database)
	assert.Equal(t, "example", systemSettings.ConnectionString)
}

func TestSystemSettingsOpenSQLiteDatabase(t *testing.T) {
	settings := SystemSettings{
		Database:         "SQLite",
		ConnectionString: filepath.Join(t.TempDir(), "weewoo.db"),
	}
	db, err := settings.OpenDatabase()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Ping())
	assert.Equal(t, 1, db.Stats().MaxOpenConnections)

	var foreignKeys int
	require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys))
	assert.Equal(t, 1, foreignKeys)
}

func TestSystemSettingsRejectsUnknownDatabase(t *testing.T) {
	_, err := (&SystemSettings{Database: "mysql", ConnectionString: "ignored"}).OpenDatabase()
	assert.EqualError(t, err, "database must be either postgresql or sqlite")
}
