package config

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
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
}
