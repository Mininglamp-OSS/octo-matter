package config

import (
	"strings"
	"testing"
)

func TestValidate_RequiresMySQLDSN(t *testing.T) {
	c := &Config{AppEnv: AppEnvDev, AuthURL: "http://auth", ServerPort: "8080"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "MYSQL_DSN") {
		t.Fatalf("expected MYSQL_DSN error, got %v", err)
	}
}

func TestValidate_RequiresAuthURL(t *testing.T) {
	c := &Config{AppEnv: AppEnvDev, MySQLDSN: "dsn", ServerPort: "8080"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "AUTH_URL") {
		t.Fatalf("expected AUTH_URL error, got %v", err)
	}
}

func TestValidate_ProdRequiresRedisURL(t *testing.T) {
	c := &Config{AppEnv: AppEnvProd, MySQLDSN: "dsn", AuthURL: "http://auth", InternalKey: "key", ServerPort: "8080"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "REDIS_URL") {
		t.Fatalf("expected REDIS_URL error, got %v", err)
	}
}

func TestValidate_ProdRequiresInternalKey(t *testing.T) {
	c := &Config{AppEnv: AppEnvProd, MySQLDSN: "dsn", AuthURL: "http://auth", RedisURL: "redis://x", ServerPort: "8080"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "AUTH_INTERNAL_KEY") {
		t.Fatalf("expected AUTH_INTERNAL_KEY error, got %v", err)
	}
}

func TestValidate_HappyPath_Dev(t *testing.T) {
	c := &Config{AppEnv: AppEnvDev, MySQLDSN: "dsn", AuthURL: "http://auth", ServerPort: "8080"}
	if err := c.Validate(); err != nil {
		t.Fatalf("dev should pass, got %v", err)
	}
}

func TestValidate_HappyPath_Prod(t *testing.T) {
	c := &Config{AppEnv: AppEnvProd, MySQLDSN: "dsn", AuthURL: "http://auth", RedisURL: "redis://x", InternalKey: "key", ServerPort: "8080"}
	if err := c.Validate(); err != nil {
		t.Fatalf("prod should pass, got %v", err)
	}
}
