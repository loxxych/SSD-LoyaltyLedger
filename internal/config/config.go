package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ServerHost                 string `yaml:"server_host"`
	ServerPort                 int    `yaml:"server_port"`
	LogLevel                   string `yaml:"log_level"`
	AccrualIntervalSeconds     int    `yaml:"accrual_interval_seconds"`
	WorkerConcurrency          int    `yaml:"worker_concurrency"`
	DatabaseURL                string `yaml:"database_url"`
	OrderServiceURL            string `yaml:"order_service_url"`
	OrderServiceAPIKey         string `yaml:"-"`
	OrderServiceTimeoutSeconds int    `yaml:"order_service_timeout_seconds"`
	TokenTTLHours              int    `yaml:"token_ttl_hours"`
	RewardPercent              int64  `yaml:"reward_percent"`
	AdminLogin                 string `yaml:"-"`
	AdminPassword              string `yaml:"-"`
}

func Load() (*Config, error) {
	c := &Config{ServerHost: "127.0.0.1", ServerPort: 8080, LogLevel: "info", AccrualIntervalSeconds: 3,
		WorkerConcurrency: 5, OrderServiceURL: "http://localhost:8081", OrderServiceTimeoutSeconds: 5, TokenTTLHours: 24, RewardPercent: 5}
	path := os.Getenv("CONFIG_FILE")
	if path == "" {
		path = "config.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err = decoder.Decode(c); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	for key, target := range map[string]*string{"SERVER_HOST": &c.ServerHost, "DATABASE_URL": &c.DatabaseURL,
		"ORDER_SERVICE_URL": &c.OrderServiceURL, "ORDER_SERVICE_API_KEY": &c.OrderServiceAPIKey, "ADMIN_LOGIN": &c.AdminLogin, "ADMIN_PASSWORD": &c.AdminPassword, "LOG_LEVEL": &c.LogLevel} {
		if value, ok := os.LookupEnv(key); ok {
			*target = value
		}
	}
	for key, target := range map[string]*int{"SERVER_PORT": &c.ServerPort, "ACCRUAL_INTERVAL_SECONDS": &c.AccrualIntervalSeconds,
		"WORKER_CONCURRENCY": &c.WorkerConcurrency, "ORDER_SERVICE_TIMEOUT_SECONDS": &c.OrderServiceTimeoutSeconds, "TOKEN_TTL_HOURS": &c.TokenTTLHours} {
		if value, ok := os.LookupEnv(key); ok {
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid %s", key)
			}
			*target = n
		}
	}
	if value, ok := os.LookupEnv("REWARD_PERCENT"); ok {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, errors.New("invalid REWARD_PERCENT")
		}
		c.RewardPercent = n
	}
	if c.ServerPort < 1 || c.ServerPort > 65535 || c.WorkerConcurrency < 1 || c.WorkerConcurrency > 100 || c.AccrualIntervalSeconds < 1 || c.AccrualIntervalSeconds > 3600 || c.OrderServiceTimeoutSeconds < 1 || c.OrderServiceTimeoutSeconds > 60 || c.TokenTTLHours < 1 || c.TokenTTLHours > 720 || c.RewardPercent < 0 || c.RewardPercent > 100 {
		return nil, errors.New("invalid numeric configuration")
	}
	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	u, err := url.Parse(c.OrderServiceURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid ORDER_SERVICE_URL")
	}
	if (c.AdminLogin == "") != (c.AdminPassword == "") {
		return nil, errors.New("ADMIN_LOGIN and ADMIN_PASSWORD must be set together")
	}
	if !strings.EqualFold(c.LogLevel, "info") && !strings.EqualFold(c.LogLevel, "debug") {
		return nil, errors.New("LOG_LEVEL must be info or debug")
	}
	return c, nil
}
func (c *Config) Address() string { return net.JoinHostPort(c.ServerHost, strconv.Itoa(c.ServerPort)) }
func (c *Config) Interval() time.Duration {
	return time.Duration(c.AccrualIntervalSeconds) * time.Second
}
