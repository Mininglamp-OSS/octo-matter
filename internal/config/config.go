package config

import (
	"fmt"
	"os"
)

// AppEnv distinguishes development from production for validation strictness.
type AppEnv string

const (
	AppEnvDev  AppEnv = "dev"
	AppEnvProd AppEnv = "prod"
)

// Config holds the application configuration loaded from environment variables.
type Config struct {
	AppEnv       AppEnv
	MySQLDSN     string
	RedisURL     string
	AuthURL      string // octo-auth base URL for token verification
	InternalKey  string // pre-shared key for calling octo-auth internal endpoints
	ServerPort   string
	WuKongIMURL  string // WuKongIM API URL for sending messages
	NotifyBotUID string // system notification bot UID
}

// Load reads configuration from environment.
func Load() *Config {
	env := AppEnv(envOrDefault("APP_ENV", string(AppEnvDev)))
	return &Config{
		AppEnv:      env,
		MySQLDSN:    devDefault(env, "MYSQL_DSN", "todo:todo@tcp(127.0.0.1:3306)/octo_todo?charset=utf8mb4&parseTime=true"),
		RedisURL:    devDefault(env, "REDIS_URL", "redis://127.0.0.1:6379/0"),
		AuthURL:     devDefault(env, "AUTH_URL", "http://127.0.0.1:8080"),
		InternalKey: os.Getenv("AUTH_INTERNAL_KEY"),
		ServerPort:  envOrDefault("SERVER_PORT", "8080"),
		WuKongIMURL:  devDefault(env, "WUKONGIM_API_URL", "http://127.0.0.1:5001"),
		NotifyBotUID: envOrDefault("NOTIFY_BOT_UID", "notification"),
	}
}

// Validate returns an error if any required field is missing.
func (c *Config) Validate() error {
	if c.AppEnv != AppEnvDev && c.AppEnv != AppEnvProd {
		return fmt.Errorf("APP_ENV must be 'dev' or 'prod', got %q", c.AppEnv)
	}
	if c.MySQLDSN == "" {
		return fmt.Errorf("MYSQL_DSN is required")
	}
	if c.AuthURL == "" {
		return fmt.Errorf("AUTH_URL is required")
	}
	if c.AppEnv == AppEnvProd && c.RedisURL == "" {
		return fmt.Errorf("REDIS_URL is required when APP_ENV=prod")
	}
	if c.AppEnv == AppEnvProd && c.InternalKey == "" {
		return fmt.Errorf("AUTH_INTERNAL_KEY is required when APP_ENV=prod")
	}
	if c.AppEnv == AppEnvDev && c.InternalKey == "" {
		fmt.Println("WARN: AUTH_INTERNAL_KEY not set — octo-auth calls will lack internal key authentication")
	}
	if c.ServerPort == "" {
		return fmt.Errorf("SERVER_PORT is required")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func devDefault(env AppEnv, key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if env == AppEnvDev {
		return fallback
	}
	return ""
}
