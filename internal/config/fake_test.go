package config

import (
	"testing"
)

func TestConfigFunctionsFake(t *testing.T) {
	config := NewFakeConfig()
	defer config.Close()

	testConfigFunctions(t, config)
}

func TestDataSourceFunctionsFake(t *testing.T) {
	config := NewFakeConfig()
	defer config.Close()

	testDataSourceFunctions(t, config)
}

func TestServiceFunctionsFake(t *testing.T) {
	config := NewFakeConfig()
	defer config.Close()

	testServiceFunctions(t, config)
}
