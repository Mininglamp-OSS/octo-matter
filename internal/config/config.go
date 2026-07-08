package config

import (
	"fmt"
	"os"
	"strconv"
)

type AppEnv string

const (
	AppEnvDev  AppEnv = "dev"
	AppEnvProd AppEnv = "prod"
)

type Config struct {
	AppEnv              AppEnv
	MySQLDSN            string
	OctoIMURL           string // octoim base URL for auth verify + Space check + internal notify
	NotifyInternalToken string // shared secret for octoim /v1/internal/notify (X-Internal-Token)
	ServerPort          string
	LLMApiURL           string
	LLMApiKey           string
	LLMModel            string
	LLMProvider         string
	LLMTimeout          int    // seconds
	DefaultLanguage     string // runtime i18n fallback (zh-CN | en-US)

	// v2 engine knobs (sane defaults; env-overridable for ops/testing)
	PublicUIPath             string        // doorbell deep-link base (behind nginx: /matter/ui)
	OutboxDispatchInterval   time.Duration // MATTER_OUTBOX_DISPATCH_SECONDS
	OutboxRedeliverAfter     time.Duration // MATTER_OUTBOX_REDELIVER_MINUTES
	OutboxMaxRetries         uint          // MATTER_OUTBOX_MAX_RETRIES
	WatchdogInterval         time.Duration // MATTER_WATCHDOG_INTERVAL_SECONDS
	WatchdogEnabled          bool          // MATTER_WATCHDOG_ENABLED
	WatchdogReviveSilence    time.Duration // MATTER_WATCHDOG_REVIVE_MINUTES
	WatchdogLeafSLA          time.Duration // MATTER_WATCHDOG_LEAF_SLA_MINUTES
	WatchdogBlockAfterRevive time.Duration // MATTER_WATCHDOG_BLOCK_MINUTES
	ScheduleTick             time.Duration // MATTER_SCHEDULE_TICK_SECONDS
}

func Load() *Config {
	env := AppEnv(envOrDefault("APP_ENV", string(AppEnvDev)))
	return &Config{
		AppEnv:              env,
		MySQLDSN:            devDefault(env, "MYSQL_DSN", "matter:matter@tcp(127.0.0.1:3306)/octo_matters?charset=utf8mb4&parseTime=true"),
		OctoIMURL:           envOrFallback("OCTO_IM_URL", "DMWORKIM_URL", devDefaultVal(env, "http://127.0.0.1:8090")),
		NotifyInternalToken: envOrDefault("NOTIFY_INTERNAL_TOKEN", ""),
		ServerPort:          envOrDefault("SERVER_PORT", "8080"),
		LLMApiURL:           envOrDefault("LLM_API_URL", "https://api.example.com/v1"),
		LLMApiKey:           envOrDefault("LLM_API_KEY", ""),
		LLMModel:            envOrDefault("LLM_MODEL", "claude-sonnet-4-6"),
		LLMProvider:         envOrDefault("OCTO_LLM_PROVIDER", "compat"),
		LLMTimeout:          envIntOrDefault("LLM_TIMEOUT", 30),
		DefaultLanguage:     envOrDefault("OCTO_DEFAULT_LANGUAGE", "zh-CN"),

		PublicUIPath:             envOrDefault("MATTER_PUBLIC_UI_PATH", "/matter/ui"),
		OutboxDispatchInterval:   time.Duration(envIntOrDefault("MATTER_OUTBOX_DISPATCH_SECONDS", 3)) * time.Second,
		OutboxRedeliverAfter:     time.Duration(envIntOrDefault("MATTER_OUTBOX_REDELIVER_MINUTES", 10)) * time.Minute,
		OutboxMaxRetries:         uint(envIntOrDefault("MATTER_OUTBOX_MAX_RETRIES", 5)),
		WatchdogInterval:         time.Duration(envIntOrDefault("MATTER_WATCHDOG_INTERVAL_SECONDS", 60)) * time.Second,
		WatchdogEnabled:          envBoolOrDefault("MATTER_WATCHDOG_ENABLED", true),
		WatchdogReviveSilence:    time.Duration(envIntOrDefault("MATTER_WATCHDOG_REVIVE_MINUTES", 5)) * time.Minute,
		WatchdogLeafSLA:          time.Duration(envIntOrDefault("MATTER_WATCHDOG_LEAF_SLA_MINUTES", 60)) * time.Minute,
		WatchdogBlockAfterRevive: time.Duration(envIntOrDefault("MATTER_WATCHDOG_BLOCK_MINUTES", 15)) * time.Minute,
		ScheduleTick:             time.Duration(envIntOrDefault("MATTER_SCHEDULE_TICK_SECONDS", 30)) * time.Second,
	}
}

func (c *Config) Validate() error {
	if c.AppEnv != AppEnvDev && c.AppEnv != AppEnvProd {
		return fmt.Errorf("APP_ENV must be 'dev' or 'prod', got %q", c.AppEnv)
	}
	if c.MySQLDSN == "" {
		return fmt.Errorf("MYSQL_DSN is required")
	}
	if c.OctoIMURL == "" {
		return fmt.Errorf("OCTO_IM_URL is required")
	}
	if c.ServerPort == "" {
		return fmt.Errorf("SERVER_PORT is required")
	}
	if c.DefaultLanguage != "zh-CN" && c.DefaultLanguage != "en-US" {
		return fmt.Errorf("OCTO_DEFAULT_LANGUAGE must be 'zh-CN' or 'en-US', got %q", c.DefaultLanguage)
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBoolOrDefault(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
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

func envOrFallback(primary, fallback, def string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	if v := os.Getenv(fallback); v != "" {
		return v
	}
	return def
}

func devDefaultVal(env AppEnv, fallback string) string {
	if env == AppEnvDev {
		return fallback
	}
	return ""
}
