package main

type fakeConfig struct {
	config map[string]string
}

func (c *fakeConfig) getConfig(key string) string {
	return c.config[key]
}

func (c *fakeConfig) setConfig(key string, value string) bool {
	c.config[key] = value
	return true
}
