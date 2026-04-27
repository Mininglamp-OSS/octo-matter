package config

import (
	"strings"
	"testing"
)

func TestValidate_RequiresMySQLDSN(t *testing.T) {
	c := &Config{
		AppEnv:     AppEnvDev,
		AuthMode:   AuthModeStub,
		ServerPort: "8080",
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "MYSQL_DSN") {
		t.Fatalf("expected MYSQL_DSN error, got %v", err)
	}
}

func TestValidate_ProdRejectsStubAuth(t *testing.T) {
	c := &Config{
		AppEnv:     AppEnvProd,
		AuthMode:   AuthModeStub,
		MySQLDSN:   "dsn",
		ServerPort: "8080",
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "stub") {
		t.Fatalf("prod+stub must be rejected, got %v", err)
	}
}

func TestValidate_RemoteAuthRequiresURL(t *testing.T) {
	c := &Config{
		AppEnv:     AppEnvDev,
		AuthMode:   AuthModeRemote,
		MySQLDSN:   "dsn",
		ServerPort: "8080",
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "AUTH_URL") {
		t.Fatalf("remote mode without AUTH_URL must be rejected, got %v", err)
	}
}

func TestValidate_RejectsUnknownAuthMode(t *testing.T) {
	c := &Config{
		AppEnv:     AppEnvDev,
		AuthMode:   "bogus",
		MySQLDSN:   "dsn",
		ServerPort: "8080",
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "AUTH_MODE") {
		t.Fatalf("unknown AUTH_MODE must be rejected, got %v", err)
	}
}

func TestValidate_HappyPath_DevStub(t *testing.T) {
	c := &Config{
		AppEnv:     AppEnvDev,
		AuthMode:   AuthModeStub,
		MySQLDSN:   "dsn",
		ServerPort: "8080",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("dev+stub+dsn should pass, got %v", err)
	}
}

func TestValidate_HappyPath_ProdJWT(t *testing.T) {
	c := &Config{
		AppEnv:     AppEnvProd,
		AuthMode:   AuthModeJWT,
		MySQLDSN:   "dsn",
		JWKSURL:    "http://octo-auth:8080/.well-known/jwks.json",
		RedisURL:   "redis://127.0.0.1:6379/0",
		ServerPort: "8080",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("prod+jwt+dsn+jwks should pass, got %v", err)
	}
}

func TestValidate_HappyPath_ProdRemote(t *testing.T) {
	c := &Config{
		AppEnv:     AppEnvProd,
		AuthMode:   AuthModeRemote,
		MySQLDSN:   "dsn",
		AuthURL:    "http://auth",
		RedisURL:   "redis://127.0.0.1:6379/0",
		ServerPort: "8080",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("prod+remote+dsn+url should pass, got %v", err)
	}
}
