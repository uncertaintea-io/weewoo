package config

import "errors"

// a new fake config should be initially empty
func NewFakeConfig() Config {
	return &fakeConfig{config: map[string]string{}}
}

type fakeConfig struct {
	config map[string]string
}

func (c *fakeConfig) GetConfig(key string) (string, error) {
	if key == "" {
		return "", errors.New("key is empty, please enter a valid key")
	}
	return c.config[key], nil
}

func (c *fakeConfig) SetConfig(key string, value string) error {
	if key == "" || value == "" {
		return errors.New("key and value are required")
	}
	c.config[key] = value
	return nil
}

func (c *fakeConfig) Close() {}
