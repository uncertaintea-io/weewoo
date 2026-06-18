package config

import (
	"testing"
	"strconv"
	"github.com/stretchr/testify/assert"
)

// this tests that a key can be inserted into getConfig and a value for that key can be returned.
func testGetConfig(t *testing.T, config Config) {
	input := []string{"test1", "test2", "test3", "test4", "test5", "test6", "test7"}
	want := []string{"test1", "test2", "test3", "test4", "test5", "test6", "test7"}
	for i, key := range input {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, want[i], config.getConfig(key))
		})
	}
}

// this tests that values can be inserted into setConfig without any errors testing for edge cases like key is empty, value is empty, key already exists, value already exists. If a key or value is empty it should return an error.
func testSetConfig(t *testing.T, config Config) {
	inputKey := []string{"test1", "test2", "test3", "test4", "test5", "test6", "test7", "test8"}
	inputValue := []string{"test1", "test2", "test3", "test4", "test5", "test6", "test7", "test8"}
	want := []bool{true, true, true, true, true, true, true, true}
	for i, _ := range inputKey {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, want[i], config.setConfig(inputKey[i], inputValue[i]))
		})
	}
}
