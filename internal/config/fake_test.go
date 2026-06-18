package config

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newFakeConfig() Config {
	return &fakeConfig{
		config: map[string]string{},
	}
}

func TestConfigFunctionsFake(t *testing.T) {
	config := newFakeConfig()
	defer config.Close()

	testConfigFunctionsFake(t, config)
}

func testConfigFunctionsFake(t *testing.T, config Config) {
	// testing setConfigFake
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
	// testing getConfigFake
	for i, key := range inputKey {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			got, err := config.GetConfig(key)
			assert.NoError(t, err)
			assert.Equal(t, inputValue[i], got)
		})
	}
	// testing getConfigFake with missing key
	got, err := config.GetConfig("missing")
	assert.NoError(t, err)
	assert.Equal(t, "", got)

	// testing getConfigFake with empty key
	got, err = config.GetConfig("")
	assert.Equal(t, "", got)
	assert.EqualError(t, err, "key is empty, please enter a valid key")

	// testing setConfigFake with empty key or value
	ok, err := config.SetConfig("", "value")
	assert.False(t, ok)
	assert.Error(t, err)

	ok, err = config.SetConfig("key", "")
	assert.False(t, ok)
	assert.Error(t, err)
}
