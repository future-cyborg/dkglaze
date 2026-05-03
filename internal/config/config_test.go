package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/future-cyborg/udocker/internal/config"
)

func TestGetConfigPaths(t *testing.T) {
	paths, err := config.GetConfigPaths()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if paths.ConfigDir == "" {
		t.Error("ConfigDir should not be empty")
	}

	if paths.ConfigFile == "" {
		t.Error("ConfigFile should not be empty")
	}

	if paths.TemplatesDir == "" {
		t.Error("TemplatesDir should not be empty")
	}

	if filepath.Base(paths.ConfigFile) != "config.yml" {
		t.Errorf("expected config.yml, got %s", filepath.Base(paths.ConfigFile))
	}

	if filepath.Base(paths.TemplatesDir) != "templates" {
		t.Errorf("expected templates dir, got %s", filepath.Base(paths.TemplatesDir))
	}
}

func withTempConfigDir(t *testing.T) func() {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "udocker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	t.Setenv("UDOCKER_CONFIG_DIR", tmpDir)

	return func() {
		_ = os.RemoveAll(tmpDir)
	}
}

func TestInitConfig(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	err := config.InitConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	paths, _ := config.GetConfigPaths()

	if _, err := os.Stat(paths.ConfigFile); os.IsNotExist(err) {
		t.Error("expected config file to be created")
	}

	if _, err := os.Stat(paths.TemplatesDir); os.IsNotExist(err) {
		t.Error("expected templates dir to be created")
	}
}

func TestInitConfig_AlreadyExists(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("first init error: %v", err)
	}

	if err := config.InitConfig(); err != nil {
		t.Fatalf("second init should not error: %v", err)
	}
}

func TestLoadConfig(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Defaults.Suffix == "" {
		t.Error("expected default suffix to be set")
	}
}

func TestLoadConfig_BuildSection(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	customConfig := `defaults:
  suffix: test
build:
  templates:
    - mytemplate
  args: []
run:
  args: []
`
	if err := os.WriteFile(paths.ConfigFile, []byte(customConfig), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Defaults.Suffix != "test" {
		t.Errorf("expected suffix 'test', got '%s'", cfg.Defaults.Suffix)
	}

	if len(cfg.Build.Templates) != 1 || cfg.Build.Templates[0] != "mytemplate" {
		t.Errorf("expected templates [mytemplate], got %v", cfg.Build.Templates)
	}
}

func TestLoadConfig_PopulatesArgFromCmd(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	customConfig := `defaults:
  suffix: test
build:
  args:
    - key: MY_ARG
      cmd: echo hello
run:
  args: []
`
	if err := os.WriteFile(paths.ConfigFile, []byte(customConfig), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Build.Args) != 1 {
		t.Fatalf("expected 1 build arg, got %d", len(cfg.Build.Args))
	}

	if cfg.Build.Args[0].Value != "hello" {
		t.Errorf("expected value 'hello', got '%s'", cfg.Build.Args[0].Value)
	}
}

func TestLoadConfig_InvalidSchema(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	invalidConfig := `defaults:
  unknown_field: bad
  suffix: test
`
	if err := os.WriteFile(paths.ConfigFile, []byte(invalidConfig), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, err := config.LoadConfig()
	if err == nil {
		t.Error("expected error for invalid config, got nil")
	}
}
