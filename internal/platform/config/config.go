// Package config loads application configuration from env / .env / yaml.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	App    AppConfig
	HTTP   HTTPConfig
	DB     DBConfig
	Redis  RedisConfig
	JWT    JWTConfig
	OTel   OTelConfig
	Logger LoggerConfig
}

type AppConfig struct {
	Env             string        `mapstructure:"env"`
	Name            string        `mapstructure:"name"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type HTTPConfig struct {
	Port int `mapstructure:"port"`
}

type DBConfig struct {
	DSN      string `mapstructure:"dsn"`
	MaxConns int32  `mapstructure:"max_conns"`
	MinConns int32  `mapstructure:"min_conns"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type JWTConfig struct {
	Secret string        `mapstructure:"secret"`
	Issuer string        `mapstructure:"issuer"`
	TTL    time.Duration `mapstructure:"ttl"`
}

type OTelConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	Endpoint    string `mapstructure:"endpoint"`
	ServiceName string `mapstructure:"service_name"`
}

type LoggerConfig struct {
	Level    string `mapstructure:"level"`
	Encoding string `mapstructure:"encoding"`
}

// Load reads config from env (with .env fallback). Env vars are upper-cased
// and underscored, e.g. APP_ENV, POSTGRES_DSN.
func Load() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("app.env", "development")
	v.SetDefault("app.name", "go-backend-template")
	v.SetDefault("app.shutdown_timeout", "10s")
	v.SetDefault("http.port", 8080)
	v.SetDefault("db.max_conns", 20)
	v.SetDefault("db.min_conns", 2)
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.db", 0)
	v.SetDefault("jwt.issuer", "go-backend-template")
	v.SetDefault("jwt.ttl", "24h")
	v.SetDefault("otel.enabled", false)
	v.SetDefault("otel.endpoint", "localhost:4317")
	v.SetDefault("otel.service_name", "go-backend-template")
	v.SetDefault("logger.level", "info")
	v.SetDefault("logger.encoding", "json")

	// Env mapping: APP_ENV → app.env
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Manual binds (viper's automatic binding doesn't traverse nested keys
	// reliably for unset envs).
	binds := map[string]string{
		"app.env":              "APP_ENV",
		"app.name":             "APP_NAME",
		"app.shutdown_timeout": "APP_SHUTDOWN_TIMEOUT",
		"http.port":            "APP_PORT",
		"db.dsn":               "POSTGRES_DSN",
		"db.max_conns":         "POSTGRES_MAX_CONNS",
		"db.min_conns":         "POSTGRES_MIN_CONNS",
		"redis.addr":           "REDIS_ADDR",
		"redis.password":       "REDIS_PASSWORD",
		"redis.db":             "REDIS_DB",
		"jwt.secret":           "JWT_SECRET",
		"jwt.issuer":           "JWT_ISSUER",
		"jwt.ttl":              "JWT_TTL",
		"otel.enabled":         "OTEL_ENABLED",
		"otel.endpoint":        "OTEL_ENDPOINT",
		"otel.service_name":    "OTEL_SERVICE_NAME",
		"logger.level":         "LOG_LEVEL",
		"logger.encoding":      "LOG_ENCODING",
	}
	for k, env := range binds {
		_ = v.BindEnv(k, env)
	}

	// Optional .env (developer convenience)
	v.SetConfigName(".env")
	v.SetConfigType("env")
	v.AddConfigPath(".")
	_ = v.ReadInConfig() // ignore if missing

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.DB.DSN == "" {
		return fmt.Errorf("POSTGRES_DSN is required")
	}
	if c.JWT.Secret == "" {
		return fmt.Errorf("JWT_SECRET is required")
	}
	return nil
}
