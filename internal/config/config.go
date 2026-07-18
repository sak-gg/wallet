package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseDSN string
	Port        string
}

func Load() (Config, error) {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		return Config{}, fmt.Errorf("DATABASE_DSN environment variable is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	return Config{DatabaseDSN: dsn, Port: port}, nil
}
