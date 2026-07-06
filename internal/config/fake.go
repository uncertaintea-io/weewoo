package config

import (
	"errors"
	"fmt"
)

// a new fake config should be initially empty
func NewFakeConfig() Config {
	return &fakeConfig{config: map[string]string{}, dataSources: map[int]*DataSource{}, services: map[int]*Service{}}
}

type fakeConfig struct {
	config      map[string]string
	dataSources map[int]*DataSource
	services    map[int]*Service
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

func (c *fakeConfig) WriteDataSource(dataSource *DataSource) error {
	if dataSource.Id == 0 {
		dataSource.Id = len(c.dataSources) + 1
	}
	c.dataSources[dataSource.Id] = dataSource
	return nil
}

func (c *fakeConfig) WriteService(service *Service) error {
	if service.Id == 0 {
		service.Id = len(c.services) + 1
	}
	c.services[service.Id] = service
	return nil
}

func (c *fakeConfig) ReadService(id int) (*Service, error) {
	if _, ok := c.services[id]; !ok {
		return nil, fmt.Errorf("id not found in database")
	}
	return c.services[id], nil
}

// this function reads all the services from the fake config and returns them as a slice of service objects
func (c *fakeConfig) ReadAllServices() ([]*Service, error) {
	services := make([]*Service, 0)
	for _, service := range c.services {
		services = append(services, service)
	}
	return services, nil
}

func (c *fakeConfig) Close() {
	c.config = nil
	c.dataSources = nil
	c.services = nil
}
