package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Host     string
	Port     int
	Env      string
	Secret   string
	DB       DBConfig
	SiteURL  string
	SiteName string
}

type DBConfig struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
	SSLMode  string
}

func (db DBConfig) ConnString() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		db.User, db.Password, db.Host, db.Port, db.Name, db.SSLMode,
	)
}

func Load() *Config {
	return &Config{
		Host:   envOrDefault("GOLAB_HOST", "0.0.0.0"),
		Port:   envIntOrDefault("GOLAB_PORT", 3000),
		Env:    envOrDefault("GOLAB_ENV", "development"),
		Secret: envOrDefault("GOLAB_SECRET", "dev-secret-change-in-production"),
		DB: DBConfig{
			Host:     envOrDefault("GOLAB_DB_HOST", "localhost"),
			Port:     envIntOrDefault("GOLAB_DB_PORT", 5432),
			Name:     envOrDefault("GOLAB_DB_NAME", "golab"),
			User:     envOrDefault("GOLAB_DB_USER", "golab"),
			Password: envOrDefault("GOLAB_DB_PASSWORD", "golab-dev"),
			SSLMode:  envOrDefault("GOLAB_DB_SSLMODE", "disable"),
		},
		SiteURL:  envOrDefault("GOLAB_SITE_URL", "http://localhost:3000"),
		SiteName: envOrDefault("GOLAB_SITE_NAME", "GoLab"),
	}
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func (c *Config) IsDev() bool {
	return c.Env == "development"
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
