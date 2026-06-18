package config

import (
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Config interface {
	GetConfig(key string) (string, error)
	SetConfig(key string, value string) error
	Close()
}
