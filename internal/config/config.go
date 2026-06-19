package config

import (
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/yaml.v3"
)

// this struct tells config how to connect to the database using yaml files
type SystemSettings struct {
	DatabaseURL string `yaml:"database_url"`
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
	Close()
}
