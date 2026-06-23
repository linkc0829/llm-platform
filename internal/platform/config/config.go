package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	App    AppConfig
	HTTP   HTTPConfig
	Logger LoggerConfig
	OpenAI OpenAIConfig
}

type AppConfig struct {
	Env             string        `mapstructure:"env"`
	Name            string        `mapstructure:"name"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type HTTPConfig struct {
	Port int `mapstructure:"port"`
}

type LoggerConfig struct {
	Level    string `mapstructure:"level"`
	Encoding string `mapstructure:"encoding"`
}

type OpenAIConfig struct {
	APIKey  string `mapstructure:"api_key"`
	LLMMode string `mapstructure:"llm_mode"`
}

func LoadKB() (*Config, error) {
	v := newViper()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.OpenAI.LLMMode != "fake" && cfg.OpenAI.APIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required")
	}
	return &cfg, nil
}

func newViper() *viper.Viper {
	v := viper.New()

	v.SetDefault("app.env", "development")
	v.SetDefault("app.name", "knowledge-base-qa-bot")
	v.SetDefault("app.shutdown_timeout", "10s")
	v.SetDefault("http.port", 8080)
	v.SetDefault("logger.level", "info")
	v.SetDefault("logger.encoding", "json")
	v.SetDefault("openai.llm_mode", "openai")

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	binds := map[string]string{
		"app.env":              "APP_ENV",
		"app.name":             "APP_NAME",
		"app.shutdown_timeout": "APP_SHUTDOWN_TIMEOUT",
		"http.port":            "APP_PORT",
		"logger.level":         "LOG_LEVEL",
		"logger.encoding":      "LOG_ENCODING",
		"openai.api_key":       "OPENAI_API_KEY",
		"openai.llm_mode":      "KB_LLM_MODE",
	}
	for k, env := range binds {
		_ = v.BindEnv(k, env)
	}

	v.SetConfigName(".env")
	v.SetConfigType("env")
	v.AddConfigPath(".")
	_ = v.ReadInConfig()
	applyEnvFileAliases(v, binds)

	return v
}

func applyEnvFileAliases(v *viper.Viper, binds map[string]string) {
	for key, env := range binds {
		if _, ok := os.LookupEnv(env); ok {
			continue
		}
		if v.InConfig(env) {
			v.Set(key, v.Get(env))
		}
	}
}
