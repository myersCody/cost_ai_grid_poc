package config

import (
	"fmt"
	"os"
	"strings"
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
	LogFormat               string
	IngestListenAddr        string
	MetricsPort             string
	CustomMetricsConfigPath string
	AuthIssuerURL           string
	DebugDashboard     bool
	DisabledComponents map[string]bool
}

// DiagnosticInfo returns config values safe to expose via the debug API (no secrets).
type DiagnosticInfo struct {
	OSACBaseURL       string `json:"osac_base_url"`
	InventoryDBHost   string `json:"inventory_db_host"`
	ReconcileInterval string `json:"reconcile_interval"`
	SummarizeInterval string `json:"summarize_interval"`
	MeteringInterval  string `json:"metering_interval"`
	RatingInterval    string `json:"rating_interval"`
	LogLevel          string `json:"log_level"`
	LogFormat               string `json:"log_format"`
	IngestListenAddr        string `json:"ingest_listen_addr"`
	MetricsPort             string `json:"metrics_port"`
	CustomMetricsConfigPath string `json:"custom_metrics_config_path"`
	AuthIssuerURL           string `json:"auth_issuer_url"`
	DebugDashboard    bool   `json:"debug_dashboard"`
	OSACTokenSet      bool   `json:"osac_token_set"`
	OSACCACertSet     bool   `json:"osac_ca_cert_set"`
}

func (c *Config) Diagnostics() DiagnosticInfo {
	dbHost := c.InventoryDBURL
	if idx := findCredEnd(dbHost); idx > 0 {
		dbHost = dbHost[:idx] + "****@" + dbHost[idx:]
	}

	return DiagnosticInfo{
		OSACBaseURL:       c.OSACBaseURL,
		InventoryDBHost:   maskDBURL(c.InventoryDBURL),
		ReconcileInterval: c.ReconcileInterval.String(),
		SummarizeInterval: c.SummarizeInterval.String(),
		MeteringInterval:  "60s",
		RatingInterval:    "30s",
		LogLevel:          c.LogLevel,
		LogFormat:               c.LogFormat,
		IngestListenAddr:        c.IngestListenAddr,
		MetricsPort:             c.MetricsPort,
		CustomMetricsConfigPath: c.CustomMetricsConfigPath,
		AuthIssuerURL:           c.AuthIssuerURL,
		DebugDashboard:    c.DebugDashboard,
		OSACTokenSet:      c.OSACToken != "",
		OSACCACertSet:     c.OSACCACert != "",
	}
}

func maskDBURL(url string) string {
	at := -1
	for i, c := range url {
		if c == '@' {
			at = i
			break
		}
	}
	if at < 0 {
		return url
	}
	scheme := ""
	slashes := 0
	for i, c := range url {
		if c == '/' {
			slashes++
			if slashes == 2 {
				scheme = url[:i+1]
				break
			}
		}
	}
	return scheme + "****@" + url[at+1:]
}

func findCredEnd(url string) int {
	for i, c := range url {
		if c == '@' {
			return i
		}
	}
	return -1
}

func Load() *Config {
	return &Config{
		OSACBaseURL:        envOrDefault("OSAC_BASE_URL", "http://localhost:8011"),
		OSACToken:          os.Getenv("OSAC_TOKEN"),
		OSACCACert:         envOrDefault("OSAC_CA_CERT", ""),
		InventoryDBURL:     envOrDefault("INVENTORY_DB_URL", "postgres://user:pass@localhost:5434/costdb"),
		ReconcileInterval:  durationOrDefault("RECONCILE_INTERVAL", 1*time.Hour),
		SummarizeInterval:  durationOrDefault("SUMMARIZE_INTERVAL", 1*time.Hour),
		LogLevel:           envOrDefault("LOG_LEVEL", "info"),
		LogFormat:          envOrDefault("LOG_FORMAT", "text"),
		IngestListenAddr:   os.Getenv("INGEST_LISTEN_ADDR"),
		MetricsPort:        envOrDefault("METRICS_PORT", "9000"),
		CustomMetricsConfigPath: os.Getenv("CUSTOM_METRICS_CONFIG"),
		AuthIssuerURL:      os.Getenv("AUTH_ISSUER_URL"),
		DebugDashboard:     envOrDefault("DEBUG_DASHBOARD", "true") != "false",
		DisabledComponents: parseDisabledComponents(os.Getenv("DISABLE_COMPONENTS")),
	}
}

func (c *Config) ComponentDisabled(name string) bool {
	return c.DisabledComponents[name]
}

func parseDisabledComponents(s string) map[string]bool {
	m := make(map[string]bool)
	if s == "" {
		return m
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			m[part] = true
		}
	}
	return m
}

func (c *Config) Validate() error {
	osacNeeded := !c.ComponentDisabled("watcher") || !c.ComponentDisabled("reconciler")
	if osacNeeded && c.OSACBaseURL == "" {
		return fmt.Errorf("OSAC_BASE_URL is required (or disable watcher+reconciler via DISABLE_COMPONENTS)")
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
