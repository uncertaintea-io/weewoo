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
	want := []bool{true, true, true, true, true, true, true, true}
	for i, _ := range inputKey {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			got, err := config.SetConfig(inputKey[i], inputValue[i])
			assert.NoError(t, err)
			assert.Equal(t, want[i], got)
		})
	}
	// testing getConfig
	for i, key := range inputKey {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			got, err := config.GetConfig(key)
			assert.NoError(t, err)
			assert.Equal(t, inputValue[i], got)
		})
	}
}
