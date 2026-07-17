package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Matrix struct {
	Homeserver string `mapstructure:"homeserver"`
	UserID     string `mapstructure:"user_id"`
	Password   string `mapstructure:"password"`
	// AllowedServers lists the homeservers whose users may invite the bot
	// into rooms. Empty means only the bot's own homeserver.
	AllowedServers []string `mapstructure:"allowed_servers"`
}

type Database struct {
	// Type is "sqlite" or "postgres".
	Type string `mapstructure:"type"`
	// URI is a file path for sqlite, or a DSN for postgres
	// (e.g. postgres://user:pass@host:5432/db?sslmode=disable).
	URI string `mapstructure:"uri"`
}

type Config struct {
	Listen   string   `mapstructure:"listen"`
	LogLevel string   `mapstructure:"log_level"`
	DataDir  string   `mapstructure:"data_dir"`
	Matrix   Matrix   `mapstructure:"matrix"`
	Database Database `mapstructure:"database"`
	// AdminTokenHash is the argon2id hash (PHC string) that SEEDS the admin
	// password in the database on first boot. Generate with `matrix-notifier
	// token hash`; usually supplied via MATRIX_NOTIFIER_ADMIN_TOKEN_HASH.
	// Once the credential exists in the database it is authoritative —
	// change the password via the UI/ChangeAdminPassword, not this value.
	AdminTokenHash string `mapstructure:"admin_token_hash"`

	// PrometheusURL enables chart rendering for alertmanager notifications
	// on channels that opt in. Empty disables charts.
	PrometheusURL string `mapstructure:"prometheus_url"`

	// RateLimitPerSecond throttles each ingest token to this many requests
	// per second (0 disables). RateLimitBurst is the bucket size.
	RateLimitPerSecond float64 `mapstructure:"rate_limit_per_second"`
	RateLimitBurst     int     `mapstructure:"rate_limit_burst"`

	// ResetIdentity is set by the --reset-identity CLI flag, not the config
	// file: on startup, replace the account's cross-signing keys and write a
	// fresh recovery key. For when the recovery key is lost.
	ResetIdentity bool `mapstructure:"-"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetDefault("listen", ":8686")
	v.SetDefault("log_level", "info")
	v.SetDefault("data_dir", "./data")
	v.SetDefault("database.type", "sqlite")
	v.SetDefault("database.uri", "./data/notifier.db")
	// Generous per-token defaults: throttle only a genuinely runaway producer.
	v.SetDefault("rate_limit_per_second", 5.0)
	v.SetDefault("rate_limit_burst", 20)

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
	}
	v.SetEnvPrefix("MATRIX_NOTIFIER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.Matrix.Homeserver == "" {
		missing = append(missing, "matrix.homeserver")
	}
	if c.Matrix.UserID == "" {
		missing = append(missing, "matrix.user_id")
	}
	if c.Matrix.Password == "" {
		missing = append(missing, "matrix.password")
	}
	if c.AdminTokenHash == "" {
		missing = append(missing, "admin_token_hash")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config keys: %s", strings.Join(missing, ", "))
	}
	if c.Database.Type != "sqlite" && c.Database.Type != "postgres" {
		return fmt.Errorf("database.type must be \"sqlite\" or \"postgres\", got %q", c.Database.Type)
	}
	return nil
}
