package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

type Config struct {
	Server    ServerConfig    `toml:"server"`
	OpenMeteo OpenMeteoConfig `toml:"open_meteo"`
	Cache     CacheConfig     `toml:"cache"`
	Database  DatabaseConfig  `toml:"database"`
	Metrics   MetricsConfig   `toml:"metrics"`
}

type ServerConfig struct {
	ListenAddr      string   `toml:"listen_addr"`
	ReadTimeout     Duration `toml:"read_timeout"`
	WriteTimeout    Duration `toml:"write_timeout"`
	ShutdownTimeout Duration `toml:"shutdown_timeout"`
}

type OpenMeteoConfig struct {
	BaseURL        string   `toml:"base_url"`
	ArchiveBaseURL string   `toml:"archive_base_url"`
	Timezone       string   `toml:"timezone"`
	ForecastDays   int      `toml:"forecast_days"`
	RequestTimeout Duration `toml:"request_timeout"`
}

type CacheConfig struct {
	TTL                 Duration `toml:"ttl"`
	CoordinatePrecision int      `toml:"coordinate_precision"`
}

type DatabaseConfig struct {
	Host            string   `toml:"host"`
	Port            int      `toml:"port"`
	Name            string   `toml:"name"`
	User            string   `toml:"user"`
	Password        string   `toml:"password"`
	PasswordEnv     string   `toml:"password_env"`
	SSLMode         string   `toml:"sslmode"`
	MaxOpenConns    int32    `toml:"max_open_conns"`
	MaxIdleConns    int32    `toml:"max_idle_conns"`
	ConnMaxLifetime Duration `toml:"conn_max_lifetime"`
}

type MetricsConfig struct {
	Hourly []string `toml:"hourly"`
	Daily  []string `toml:"daily"`
}

func Load(path string) (Config, error) {
	cfg := defaults()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaults() Config {
	return Config{
		Server: ServerConfig{
			ListenAddr:      "0.0.0.0:8080",
			ReadTimeout:     Duration{10 * time.Second},
			WriteTimeout:    Duration{30 * time.Second},
			ShutdownTimeout: Duration{10 * time.Second},
		},
		OpenMeteo: OpenMeteoConfig{
			BaseURL:        "https://api.open-meteo.com/v1/forecast",
			ArchiveBaseURL: "https://archive-api.open-meteo.com/v1/archive",
			Timezone:       "Europe/Bucharest",
			ForecastDays:   7,
			RequestTimeout: Duration{20 * time.Second},
		},
		Cache: CacheConfig{
			TTL:                 Duration{time.Hour},
			CoordinatePrecision: 4,
		},
		Database: DatabaseConfig{
			Host:            "127.0.0.1",
			Port:            5432,
			Name:            "weather",
			User:            "weather",
			Password:        "weather_dev",
			SSLMode:         "disable",
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: Duration{30 * time.Minute},
		},
	}
}

func (c Config) Validate() error {
	var errs []string
	if c.Server.ListenAddr == "" {
		errs = append(errs, "server.listen_addr is required")
	}
	if c.OpenMeteo.BaseURL == "" {
		errs = append(errs, "open_meteo.base_url is required")
	} else if _, err := url.ParseRequestURI(c.OpenMeteo.BaseURL); err != nil {
		errs = append(errs, fmt.Sprintf("open_meteo.base_url is invalid: %v", err))
	}
	if c.OpenMeteo.ArchiveBaseURL == "" {
		errs = append(errs, "open_meteo.archive_base_url is required")
	} else if _, err := url.ParseRequestURI(c.OpenMeteo.ArchiveBaseURL); err != nil {
		errs = append(errs, fmt.Sprintf("open_meteo.archive_base_url is invalid: %v", err))
	}
	if c.OpenMeteo.Timezone == "" {
		errs = append(errs, "open_meteo.timezone is required")
	}
	if c.OpenMeteo.ForecastDays < 1 || c.OpenMeteo.ForecastDays > 16 {
		errs = append(errs, "open_meteo.forecast_days must be between 1 and 16")
	}
	if c.Cache.TTL.Duration <= 0 {
		errs = append(errs, "cache.ttl must be positive")
	}
	if c.Cache.CoordinatePrecision < 0 || c.Cache.CoordinatePrecision > 8 {
		errs = append(errs, "cache.coordinate_precision must be between 0 and 8")
	}
	if len(c.Metrics.Hourly) == 0 {
		errs = append(errs, "metrics.hourly must contain at least one metric")
	}
	if len(c.Metrics.Daily) == 0 {
		errs = append(errs, "metrics.daily must contain at least one metric")
	}
	if c.Database.Host == "" || c.Database.Port == 0 || c.Database.Name == "" || c.Database.User == "" {
		errs = append(errs, "database host, port, name and user are required")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (d DatabaseConfig) DSN() string {
	password := d.Password
	if d.PasswordEnv != "" {
		if envPassword := os.Getenv(d.PasswordEnv); envPassword != "" {
			password = envPassword
		}
	}

	values := url.Values{}
	if d.SSLMode != "" {
		values.Set("sslmode", d.SSLMode)
	}

	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(d.User, password),
		Host:     fmt.Sprintf("%s:%d", d.Host, d.Port),
		Path:     d.Name,
		RawQuery: values.Encode(),
	}
	return u.String()
}
