package config

import "errors"

// a new fake config should be initially empty
func NewFakeConfig() Config {
	return &fakeConfig{config: map[string]string{}, dataSource: map[int]*DataSource{}}
}

type fakeConfig struct {
	config map[string]string
	dataSource map[int]*DataSource
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

func (c *fakeConfig) ReadData(id int) (*DataSource, error) {
	return c.dataSource[id], nil
}

func (c *fakeConfig) WriteData(dataSource *DataSource) (int, error) {
	if dataSource.id == 0 {
		dataSource.id = len(c.dataSource) + 1
	}
	c.dataSource[dataSource.id] = dataSource
	return dataSource.id, nil
}

func (c *fakeConfig) Close() {
	c.config = nil
	c.dataSource = nil
}
