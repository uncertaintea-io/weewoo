package config

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/yaml.v3"
)

var (
	ErrServiceNotFound = errors.New("service not found")
	ErrServiceConflict = errors.New("service revision conflict")
)

// this struct tells config how to connect to the database using yaml files
type SystemSettings struct {
	DatabaseURL string `yaml:"database_url"`
}

func (s *SystemSettings) OpenDatabase() (*sql.DB, error) {
	db, err := sql.Open("pgx", s.DatabaseURL)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func ReadSystemSettings(filename string) (*SystemSettings, error) {
	yamlFile, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var systemSettings SystemSettings
	err = yaml.Unmarshal(yamlFile, &systemSettings)
	if err != nil {
		return nil, err
	}
	return &systemSettings, nil
}

type Config interface {
	GetConfig(key string) (string, error)
	SetConfig(key string, value string) error
	ReadDataSource(id int) (*DataSource, error)
	WriteDataSource(dataSource *DataSource) error
	WriteService(service *Service) error
	UpdateService(ctx context.Context, service *Service, expectedRevision int64, changedBy string) error
	ResetServiceBaseline(ctx context.Context, id int, expectedRevision int64, changedBy string) (*Service, error)
	ReadService(id int) (*Service, error)
	ReadAllServices() ([]*Service, error)
	ReadServiceHistory(id int) ([]ServiceChange, error)
	DeleteService(id int) error
	Close()
}

type DataSource struct {
	Id              int
	DataType        string
	URL             url.URL
	PollingInterval time.Duration
}

type Service struct {
	Id              int           `json:"id"`
	Name            string        `json:"name"`
	PrometheusURL   string        `json:"prometheusUrl"`
	LoadQuery       string        `json:"loadQuery"`
	LatencyQuery    string        `json:"latencyQuery"`
	Interval        time.Duration `json:"interval"`
	Paused          bool          `json:"paused"`
	Revision        int64         `json:"revision"`
	Generation      int64         `json:"generation"`
	BaselineResetAt time.Time     `json:"baselineResetAt,omitempty"`
}

type ServiceChange struct {
	ServiceID        int       `json:"serviceId"`
	PreviousRevision int64     `json:"previousRevision"`
	NewRevision      int64     `json:"newRevision"`
	ChangedAt        time.Time `json:"changedAt"`
	ChangedBy        string    `json:"changedBy"`
	Material         bool      `json:"material"`
	Previous         Service   `json:"previous"`
	Current          Service   `json:"current"`
}
