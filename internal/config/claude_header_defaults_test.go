package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigOptional_ClaudeHeaderDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
claude-header-defaults:
  user-agent: "  claude-cli/2.1.70 (external, cli)  "
  package-version: "  0.80.0  "
  runtime-version: "  v24.5.0  "
  os: "  MacOS  "
  arch: "  arm64  "
  timeout: "  900  "
  stabilize-device-profile: false
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if got := cfg.ClaudeHeaderDefaults.UserAgent; got != "claude-cli/2.1.70 (external, cli)" {
		t.Fatalf("UserAgent = %q, want %q", got, "claude-cli/2.1.70 (external, cli)")
	}
	if got := cfg.ClaudeHeaderDefaults.PackageVersion; got != "0.80.0" {
		t.Fatalf("PackageVersion = %q, want %q", got, "0.80.0")
	}
	if got := cfg.ClaudeHeaderDefaults.RuntimeVersion; got != "v24.5.0" {
		t.Fatalf("RuntimeVersion = %q, want %q", got, "v24.5.0")
	}
	if got := cfg.ClaudeHeaderDefaults.OS; got != "MacOS" {
		t.Fatalf("OS = %q, want %q", got, "MacOS")
	}
	if got := cfg.ClaudeHeaderDefaults.Arch; got != "arm64" {
		t.Fatalf("Arch = %q, want %q", got, "arm64")
	}
	if got := cfg.ClaudeHeaderDefaults.Timeout; got != "900" {
		t.Fatalf("Timeout = %q, want %q", got, "900")
	}
	if cfg.ClaudeHeaderDefaults.StabilizeDeviceProfile == nil {
		t.Fatal("StabilizeDeviceProfile = nil, want non-nil")
	}
	if got := *cfg.ClaudeHeaderDefaults.StabilizeDeviceProfile; got {
		t.Fatalf("StabilizeDeviceProfile = %v, want false", got)
	}
}

func TestLoadConfigOptional_ClaudeHeaderDefaultsBetaRules(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
claude-header-defaults:
  beta-rules:
    - provider: "  custom  "
      endpoint-host:
        - "  ZenMux.AI  "
        - "zenmux.ai"
      model: "  deepseek-v4-*  "
      remove:
        - "  fast-mode-2026-02-01  "
        - "fast-mode-2026-02-01"
      add:
        - "  custom-beta-2026-04-26  "
        - "custom-beta-2026-04-26"
    - provider: "noop"
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if len(cfg.ClaudeHeaderDefaults.BetaRules) != 1 {
		t.Fatalf("len(BetaRules) = %d, want 1", len(cfg.ClaudeHeaderDefaults.BetaRules))
	}
	rule := cfg.ClaudeHeaderDefaults.BetaRules[0]
	if rule.Provider != "custom" {
		t.Fatalf("Provider = %q, want %q", rule.Provider, "custom")
	}
	if len(rule.EndpointHosts) != 1 || rule.EndpointHosts[0] != "zenmux.ai" {
		t.Fatalf("EndpointHosts = %v, want [zenmux.ai]", rule.EndpointHosts)
	}
	if rule.Model != "deepseek-v4-*" {
		t.Fatalf("Model = %q, want %q", rule.Model, "deepseek-v4-*")
	}
	if len(rule.Remove) != 1 || rule.Remove[0] != "fast-mode-2026-02-01" {
		t.Fatalf("Remove = %v, want [fast-mode-2026-02-01]", rule.Remove)
	}
	if len(rule.Add) != 1 || rule.Add[0] != "custom-beta-2026-04-26" {
		t.Fatalf("Add = %v, want [custom-beta-2026-04-26]", rule.Add)
	}
}
