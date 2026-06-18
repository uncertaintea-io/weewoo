package config

import (
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Config interface {
	getConfig(key string) string
	setConfig(key string, value string) bool
	Close()
}
