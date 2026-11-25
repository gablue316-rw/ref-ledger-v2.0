package database

import (
	"testing"
	"time"
)

func TestValidateLogCommands(t *testing.T) {
	now := time.Now().UTC()
	policy := logCommandPolicy{environment: "development", actors: map[string]bool{"monitor": true}, tenants: map[string]bool{"tenant-a": true}}
	command := logCommand{
		ID: "b2e39411-9629-4ebe-84b9-7c5a8171a962", RequestID: "b2e39411-9629-4ebe-84b9-7c5a8171a962",
		Version: 1, WorkerEnvironment: "development", Environment: "development", Actor: "monitor",
		Type: "clear_logs", Scope: "tenant", TenantID: "tenant-a", Confirmation: "CLEAR development", Cutoff: now,
	}
	if reason := validateLogCommand(command, policy, now); reason != "" {
		t.Fatal(reason)
	}
	tests := []struct {
		name   string
		change func(*logCommand)
	}{
		{"request id", func(c *logCommand) { c.RequestID = "different" }},
		{"version", func(c *logCommand) { c.Version = 2 }},
		{"environment", func(c *logCommand) { c.Environment = "production" }},
		{"actor", func(c *logCommand) { c.Actor = "untrusted" }},
		{"tenant", func(c *logCommand) { c.TenantID = "tenant-b" }},
		{"empty tenant", func(c *logCommand) { c.TenantID = "" }},
		{"unknown scope", func(c *logCommand) { c.Scope = "all" }},
		{"ambiguous environment scope", func(c *logCommand) { c.Scope = "environment" }},
		{"unauthorized environment scope", func(c *logCommand) { c.Scope, c.TenantID = "environment", "" }},
		{"confirmation", func(c *logCommand) { c.Confirmation = "" }},
		{"cutoff", func(c *logCommand) { c.Cutoff = now.Add(time.Second) }},
		{"type", func(c *logCommand) { c.Type = "unknown" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := command
			test.change(&changed)
			if validateLogCommand(changed, policy, now) == "" {
				t.Fatal("invalid command accepted")
			}
		})
	}
	environmentClear := command
	environmentClear.Scope, environmentClear.TenantID = "environment", ""
	policy.allowAllTenants = true
	if reason := validateLogCommand(environmentClear, policy, now); reason != "" {
		t.Fatal(reason)
	}
	environmentClear.TenantID = "tenant-a"
	if reason := validateLogCommand(environmentClear, policy, now); reason != "invalid_scope" {
		t.Fatalf("ambiguous environment clear: %q", reason)
	}
	policy.tenants[""] = true
	command.TenantID = ""
	if reason := validateLogCommand(command, policy, now); reason != "invalid_scope" {
		t.Fatalf("empty tenant clear: %q", reason)
	}
	command.Type, command.Scope, command.TenantID, command.Threshold = "set_capture_threshold", "environment", "", LogLevelDebug
	if reason := validateLogCommand(command, policy, now); reason != "" {
		t.Fatal(reason)
	}
	command.Threshold = 5
	if validateLogCommand(command, policy, now) == "" {
		t.Fatal("invalid threshold accepted")
	}
}

func TestLogCommandWorkerRequiresExplicitPolicy(t *testing.T) {
	t.Setenv("REF_LEDGER_LOG_COMMANDS_ENABLED", "true")
	t.Setenv("REF_LEDGER_LOG_COMMAND_ACTORS", "monitor")
	t.Setenv("REF_LEDGER_LOG_COMMAND_TENANTS", "")
	if _, enabled, err := configuredLogCommandPolicy(); err == nil || enabled {
		t.Fatal("worker enabled without tenant allowlist")
	}
	t.Setenv("REF_LEDGER_LOG_COMMAND_TENANTS", "tenant-a,tenant-b")
	if _, enabled, err := configuredLogCommandPolicy(); err != nil || !enabled {
		t.Fatalf("valid policy rejected: %v", err)
	}
	for _, setting := range []string{"", "false", "true"} {
		t.Setenv("REF_LEDGER_LOG_COMMAND_ALLOW_ALL_TENANTS", setting)
		policy, enabled, err := configuredLogCommandPolicy()
		if err != nil || !enabled || policy.allowAllTenants != (setting == "true") {
			t.Fatalf("setting %q: policy=%+v enabled=%v err=%v", setting, policy, enabled, err)
		}
	}
	t.Setenv("REF_LEDGER_LOG_COMMAND_ALLOW_ALL_TENANTS", "yes")
	if _, enabled, err := configuredLogCommandPolicy(); err == nil || enabled {
		t.Fatal("invalid all-tenant authorization accepted")
	}
}
