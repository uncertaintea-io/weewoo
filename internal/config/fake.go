package config

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// a new fake config should be initially empty
func NewFakeConfig() Config {
	return &fakeConfig{config: map[string]string{}, dataSources: map[int]*DataSource{}, services: map[int]*Service{}, history: map[int][]ServiceChange{}}
}

type fakeConfig struct {
	config      map[string]string
	dataSources map[int]*DataSource
	services    map[int]*Service
	history     map[int][]ServiceChange
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
		service.Revision = 1
		service.Generation = 1
	}
	c.services[service.Id] = service
	return nil
}

func (c *fakeConfig) UpdateService(_ context.Context, service *Service, expectedRevision int64, changedBy string) error {
	current, ok := c.services[service.Id]
	if !ok {
		return ErrServiceNotFound
	}
	if current.Revision != expectedRevision {
		return ErrServiceConflict
	}
	previous := *current
	material := previous.PrometheusURL != service.PrometheusURL ||
		previous.LoadQuery != service.LoadQuery || previous.LatencyQuery != service.LatencyQuery ||
		previous.Interval != service.Interval
	service.Revision = previous.Revision + 1
	service.Generation = previous.Generation
	if material {
		service.Generation++
		service.BaselineResetAt = time.Now().UTC()
	} else {
		service.BaselineResetAt = previous.BaselineResetAt
	}
	copy := *service
	c.services[service.Id] = &copy
	c.history[service.Id] = append(c.history[service.Id], ServiceChange{
		ServiceID: service.Id, PreviousRevision: previous.Revision, NewRevision: service.Revision,
		ChangedAt: time.Now().UTC(), ChangedBy: changedBy, Material: material,
		Previous: previous, Current: copy,
	})
	return nil
}

func (c *fakeConfig) ReadServiceHistory(id int) ([]ServiceChange, error) {
	if _, ok := c.services[id]; !ok {
		return nil, ErrServiceNotFound
	}
	return append([]ServiceChange{}, c.history[id]...), nil
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

func (c *fakeConfig) DeleteService(id int) error {
	if _, ok := c.services[id]; !ok {
		return fmt.Errorf("id not found in database")
	}
	delete(c.services, id)
	return nil
}

func (c *fakeConfig) Close() {
	c.config = nil
	c.dataSources = nil
	c.services = nil
}
