package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	OSACBaseURL        string
	OSACToken          string
	OSACCACert         string
	InventoryDBURL     string
	ReconcileInterval  time.Duration
	SummarizeInterval  time.Duration
	LogLevel           string
}

func Load() *Config {
	return &Config{
		OSACBaseURL:       envOrDefault("OSAC_BASE_URL", "http://localhost:8011"),
		OSACToken:         os.Getenv("OSAC_TOKEN"),
		OSACCACert:        envOrDefault("OSAC_CA_CERT", ""),
		InventoryDBURL:    envOrDefault("INVENTORY_DB_URL", "postgres://user:pass@localhost:5434/costdb"),
		ReconcileInterval: durationOrDefault("RECONCILE_INTERVAL", 1*time.Hour),
		SummarizeInterval: durationOrDefault("SUMMARIZE_INTERVAL", 1*time.Hour),
		LogLevel:          envOrDefault("LOG_LEVEL", "info"),
	}
}

func (c *Config) Validate() error {
	if c.OSACBaseURL == "" {
		return fmt.Errorf("OSAC_BASE_URL is required")
	}
	if c.InventoryDBURL == "" {
		return fmt.Errorf("INVENTORY_DB_URL is required")
	}
	return nil
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func durationOrDefault(key string, defaultVal time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return defaultVal
}
