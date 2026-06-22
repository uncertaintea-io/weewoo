package config

import (
	"testing"
)

func newFakeConfig() Config {
	return &fakeConfig{
		config: map[string]string{}, dataSource: map[int]*DataSource{},

	}
}

func TestConfigFunctionsFake(t *testing.T) {
	config := newFakeConfig()
	defer config.Close()

	testConfigFunctions(t, config)
}

func TestDataSourceFunctionsFake(t *testing.T) {
	config := newFakeConfig()
	defer config.Close()

	testDataSourceFunctions(t, config)
}
