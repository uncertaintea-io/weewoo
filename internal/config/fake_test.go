package config

import (
	"testing"
)

func newFakeConfig() Config {
	return &fakeConfig{
		config: map[string]string{},
	}
}

func TestConfigFunctionsFake(t *testing.T) {
	config := newFakeConfig()
	defer config.Close()

	testConfigFunctions(t, config)
}
