package config

import (
	"database/sql"
	"net/url"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/yaml.v3"
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
	ReadService(id int) (*Service, error)
	Close()
}

type DataSource struct {
	Id              int
	DataType        string
	URL             url.URL
	PollingInterval time.Duration
}

type Service struct {
	Id              int
	Name            string
	PrometheusURL   string
	LoadQuery       string
	LatencyQuery    string
	IntervalSeconds int
}
