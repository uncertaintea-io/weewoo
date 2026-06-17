package main

import (
	"testing"
	"strconv"
	"github.com/stretchr/testify/assert"
)

// this tests that a key can be inserted into getConfig and a value for that key can be returned.
func TestGetConfig(t *testing.T) {

	config := connect()
	defer config.Close()
	input := []string{"test1", "test2", "Tony", "", "this_doesnt_exist"}
	want := []string{"test1", "test2", "Tony is a good boy", "key is empty, please enter a valid key", ""}
	for i, key := range input {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, want[i], config.getConfig(key))
		})
	}
}

// this tests that values can be inserted into setConfig without any errors testing for edge cases like key is empty, value is empty, key already exists, value already exists. If a key or value is empty it should return an error.
func TestSetConfig(t *testing.T) {

	config := connect()
	defer config.Close()
	inputKey := []string{"key1", "key2", "key3", "key4", "key5", "", "null"}
	inputValue := []string{"value1", "value2", "value3", "value4", "value5", "value6", ""}
	want := []bool{true, true, true, true, true, false, false}
	for i, _ := range inputKey {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, want[i], config.setConfig(inputKey[i], inputValue[i]))
		})
	}
}
