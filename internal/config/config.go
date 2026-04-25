package config

import "os"

// Config holds the application configuration loaded from environment variables.
type Config struct {
	MySQLDSN   string
	RedisURL   string
	AuthURL    string
	BotToken   string
	ServerPort string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		MySQLDSN:   getEnv("MYSQL_DSN", "todo:todo@tcp(127.0.0.1:3306)/octo_todo?charset=utf8mb4&parseTime=true"),
		RedisURL:   getEnv("REDIS_URL", "redis://127.0.0.1:6379/0"),
		AuthURL:    getEnv("AUTH_URL", "http://127.0.0.1:8090/internal/v1"),
		BotToken:   getEnv("BOT_TOKEN", ""),
		ServerPort: getEnv("SERVER_PORT", "8080"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
