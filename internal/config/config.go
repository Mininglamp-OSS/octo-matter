package config

import (
	"fmt"
	"os"
)

type AppEnv string

const (
	AppEnvDev  AppEnv = "dev"
	AppEnvProd AppEnv = "prod"
)

type Config struct {
	AppEnv      AppEnv
	MySQLDSN    string
	DmworkIMURL string // dmworkim base URL for auth verify + Space check
	WuKongIMURL string // WuKongIM API URL for sending notifications
	ServerPort  string
}

func Load() *Config {
	env := AppEnv(envOrDefault("APP_ENV", string(AppEnvDev)))
	return &Config{
		AppEnv:      env,
		MySQLDSN:    devDefault(env, "MYSQL_DSN", "todo:todo@tcp(127.0.0.1:3306)/octo_todo?charset=utf8mb4&parseTime=true"),
		DmworkIMURL: devDefault(env, "DMWORKIM_URL", "http://127.0.0.1:8090"),
		WuKongIMURL: devDefault(env, "WUKONGIM_URL", "http://127.0.0.1:5001"),
		ServerPort:  envOrDefault("SERVER_PORT", "8080"),
	}
}

func (c *Config) Validate() error {
	if c.AppEnv != AppEnvDev && c.AppEnv != AppEnvProd {
		return fmt.Errorf("APP_ENV must be 'dev' or 'prod', got %q", c.AppEnv)
	}
	if c.MySQLDSN == "" {
		return fmt.Errorf("MYSQL_DSN is required")
	}
	if c.DmworkIMURL == "" {
		return fmt.Errorf("DMWORKIM_URL is required")
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
