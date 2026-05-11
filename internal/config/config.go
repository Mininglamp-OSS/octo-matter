// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

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
	OctoIMURL         string // octoim base URL for auth verify + Space check + internal notify
	NotifyInternalToken string // shared secret for octoim /v1/internal/notify (X-Internal-Token)
	ServerPort          string
	LLMApiURL           string
	LLMApiKey           string
	LLMModel            string
	LLMTimeout          int // seconds
}

func Load() *Config {
	env := AppEnv(envOrDefault("APP_ENV", string(AppEnvDev)))
	return &Config{
		AppEnv:              env,
		MySQLDSN:            devDefault(env, "MYSQL_DSN", "matter:matter@tcp(127.0.0.1:3306)/octo_matters?charset=utf8mb4&parseTime=true"),
		OctoIMURL:         devDefault(env, "OCTO_IM_URL", "http://127.0.0.1:8090"),
		NotifyInternalToken: envOrDefault("NOTIFY_INTERNAL_TOKEN", ""),
		ServerPort:          envOrDefault("SERVER_PORT", "8080"),
		LLMApiURL:           envOrDefault("LLM_API_URL", "https://api.example.com/v1"),
		LLMApiKey:           envOrDefault("LLM_API_KEY", ""),
		LLMModel:            envOrDefault("LLM_MODEL", "claude-sonnet-4-6"),
		LLMTimeout:          envIntOrDefault("LLM_TIMEOUT", 30),
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

func devDefault(env AppEnv, key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if env == AppEnvDev {
		return fallback
	}
	return ""
}
