package main

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

// this tests that the fakeConfig struct returns a non-nil value.
func TestFakeConfig(t *testing.T) {
	config := &fakeConfig{config: map[string]string{"test1": "test1"}}
	assert.NotNil(t, config)
}
