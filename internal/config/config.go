package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

var (
	ErrServiceNotFound = errors.New("service not found")
	ErrServiceConflict = errors.New("service revision conflict")
)

// MinimumServiceInterval bounds Prometheus range queries to the batch size the
// historical importer is designed to handle.
var MinimumServiceInterval = 15 * time.Second

// this struct tells config how to connect to the database using yaml files
type SystemSettings struct {
	Database    string `yaml:"database"`
	DatabaseURL string `yaml:"database_url"`
}

func (s *SystemSettings) OpenDatabase() (*sql.DB, error) {
	var driver string
	switch strings.ToLower(strings.TrimSpace(s.Database)) {
	case "postgresql":
		driver = "pgx"
	case "sqlite":
		driver = "sqlite"
	default:
		return nil, fmt.Errorf("database must be either postgresql or sqlite")
	}
	if strings.TrimSpace(s.DatabaseURL) == "" {
		return nil, fmt.Errorf("database_url is required")
	}
	db, err := sql.Open(driver, s.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if driver == "sqlite" {
		// A WeeWoo process owns its SQLite file. Keeping one connection makes
		// multi-step publication and baseline changes serialize consistently.
		db.SetMaxOpenConns(1)
		for _, pragma := range []string{
			`PRAGMA foreign_keys = ON`,
			`PRAGMA busy_timeout = 5000`,
			`PRAGMA journal_mode = WAL`,
		} {
			if _, err := db.Exec(pragma); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("configure sqlite database: %w", err)
			}
		}
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
	SetConfigs(values map[string]string) error
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
