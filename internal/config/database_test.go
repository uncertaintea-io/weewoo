package main

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

// this tests that the connect function returns a non-nil value.
func TestConnect(t *testing.T) {
	config := connect()
	defer config.Close()
	assert.NotNil(t, config)
}
