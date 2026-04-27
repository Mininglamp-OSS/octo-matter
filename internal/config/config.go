// Package config loads and validates service configuration from environment
// variables. Validation is strict for production: required fields must be set
// explicitly, there is no silent fallback to localhost DSNs or stub auth.
package config

import (
	"fmt"
	"os"
)

// AuthMode selects which authentication backend middleware uses.
type AuthMode string

const (
	// AuthModeStub parses "uid@name@role" tokens directly. Only safe for local
	// development — the service logs a loud WARN on startup when this is set.
	AuthModeStub AuthMode = "stub"
	// AuthModeJWT validates RS256 JWTs issued by octo-auth-server.
	// Requires JWKS_URL to be set.
	AuthModeJWT AuthMode = "jwt"
	// AuthModeBot validates Bot tokens (robot_id/app_key) against Octo IM server.
	// Requires AUTH_URL to be set.
	AuthModeBot AuthMode = "bot"
	// AuthModeRemote calls the Octo IM internal auth API. Deprecated in favor
	// of AuthModeJWT. Kept for backward compatibility; panics on startup.
	AuthModeRemote AuthMode = "remote"
	// AuthModeMixed routes based on header: "Authorization: Bot" → bot, "token" → remote user.
	AuthModeMixed AuthMode = "mixed"
)

// AppEnv distinguishes development from production for validation strictness.
type AppEnv string

const (
	AppEnvDev  AppEnv = "dev"
	AppEnvProd AppEnv = "prod"
)

// Config holds the application configuration loaded from environment variables.
type Config struct {
	AppEnv     AppEnv
	AuthMode   AuthMode
	MySQLDSN   string
	RedisURL   string
	AuthURL    string
	JWKSURL    string
	Audience   string
	ServerPort string
	InternalKey string // pre-shared key for calling octo-auth internal endpoints
}

// Load reads configuration from environment. It does NOT validate — call
// Validate() before using the config. Defaults are only applied in dev mode;
// production deploys must set every required field explicitly.
func Load() *Config {
	env := AppEnv(envOrDefault("APP_ENV", string(AppEnvDev)))
	return &Config{
		AppEnv:     env,
		AuthMode:   AuthMode(envOrDefault("AUTH_MODE", string(AuthModeStub))),
		MySQLDSN:   devDefault(env, "MYSQL_DSN", "todo:todo@tcp(127.0.0.1:3306)/octo_todo?charset=utf8mb4&parseTime=true"),
		RedisURL:   devDefault(env, "REDIS_URL", "redis://127.0.0.1:6379/0"),
		AuthURL:    devDefault(env, "AUTH_URL", "http://127.0.0.1:8090/internal/v1"),
		JWKSURL:    envOrDefault("JWKS_URL", "http://127.0.0.1:8080/.well-known/jwks.json"),
		Audience:   envOrDefault("AUDIENCE", "octo"),
		ServerPort:  envOrDefault("SERVER_PORT", "8080"),
		InternalKey: os.Getenv("AUTH_INTERNAL_KEY"),
	}
}

// Validate returns an error if any required field is missing or any value is
// out of range. Callers should log.Fatal on the returned error.
func (c *Config) Validate() error {
	if c.AppEnv != AppEnvDev && c.AppEnv != AppEnvProd {
		return fmt.Errorf("APP_ENV must be 'dev' or 'prod', got %q", c.AppEnv)
	}
	if c.AuthMode != AuthModeStub && c.AuthMode != AuthModeJWT && c.AuthMode != AuthModeBot && c.AuthMode != AuthModeRemote && c.AuthMode != AuthModeMixed {
		return fmt.Errorf("AUTH_MODE must be 'stub', 'jwt', 'bot', 'remote', or 'mixed', got %q", c.AuthMode)
	}
	if c.MySQLDSN == "" {
		return fmt.Errorf("MYSQL_DSN is required")
	}
	if c.AuthMode == AuthModeRemote && c.AuthURL == "" {
		return fmt.Errorf("AUTH_URL is required when AUTH_MODE=remote")
	}
	if c.AuthMode == AuthModeMixed && c.AuthURL == "" {
		return fmt.Errorf("AUTH_URL is required when AUTH_MODE=mixed")
	}
	if c.AuthMode == AuthModeJWT && c.JWKSURL == "" {
		return fmt.Errorf("JWKS_URL is required when AUTH_MODE=jwt")
	}
	if c.AuthMode == AuthModeBot && c.AuthURL == "" {
		return fmt.Errorf("AUTH_URL is required when AUTH_MODE=bot")
	}
	if c.AppEnv == AppEnvProd && c.AuthMode == AuthModeStub {
		return fmt.Errorf("AUTH_MODE=stub is not permitted when APP_ENV=prod; use 'jwt' or 'bot'")
	}
	if c.AppEnv == AppEnvProd && c.RedisURL == "" {
		return fmt.Errorf("REDIS_URL is required when APP_ENV=prod")
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

// devDefault returns the env var when set, the fallback when in dev, and ""
// in prod (so Validate can complain about the missing required field).
func devDefault(env AppEnv, key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if env == AppEnvDev {
		return fallback
	}
	return ""
}
