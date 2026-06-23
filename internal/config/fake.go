package config

import (
	"errors"
	"fmt"
)

// a new fake config should be initially empty
func NewFakeConfig() Config {
	return &fakeConfig{config: map[string]string{}, dataSources: map[int]*DataSource{}}
}

type fakeConfig struct {
	config map[string]string
	dataSources map[int]*DataSource
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

func (c *fakeConfig) ReadDataSource(id int) (*DataSource, error) {
	if _, ok := c.dataSources[id]; !ok {
		return nil, fmt.Errorf("id not found in database")
	}
	return c.dataSources[id], nil
}

func (c *fakeConfig) WriteDataSource(dataSource *DataSource) (int, error) {
	if dataSource.Id == 0 {
		dataSource.Id = len(c.dataSources) + 1
	}
	c.dataSources[dataSource.Id] = dataSource
	return dataSource.Id, nil
}

func (c *fakeConfig) Close() {
	c.config = nil
	c.dataSources = nil
}
