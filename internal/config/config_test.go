package config

import "testing"

func TestLoadWatchdogEnabledDefaultsToProductionSafeOn(t *testing.T) {
	t.Setenv("MATTER_WATCHDOG_ENABLED", "")

	cfg := Load()

	if !cfg.WatchdogEnabled {
		t.Fatal("expected watchdog to be enabled by default")
	}
}

func TestLoadWatchdogEnabledCanBeDisabled(t *testing.T) {
	t.Setenv("MATTER_WATCHDOG_ENABLED", "false")

	cfg := Load()

	if cfg.WatchdogEnabled {
		t.Fatal("expected MATTER_WATCHDOG_ENABLED=false to disable watchdog")
	}
}
