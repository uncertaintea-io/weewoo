package config

import "errors"

func NewFakeConfig() Config {
	return &fakeConfig{config: map[string]string{
		"test1": "test1",
		"test2": "test2",
		"test3": "test3",
		"test4": "test4",
		"test5": "test5",
		"test6": "test6",
		"test7": "test7",
	}}
}

type fakeConfig struct {
	config map[string]string
}

func (c *fakeConfig) getConfig(key string) string {
	if key == "" {
		return errors.New("key is empty, please enter a valid key").Error()
	}
	return c.config[key]
}

func (c *fakeConfig) setConfig(key string, value string) bool {
	if key == "" || value == "" {
		return false
	}
	c.config[key] = value
	return true
}

func (c *fakeConfig) Close() {}
