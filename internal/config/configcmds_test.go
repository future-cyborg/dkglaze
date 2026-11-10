package config_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/future-cyborg/ldocker/internal/config"
)

const multiProfileConfig = `defaults:
  suffix: test
  profile: default
features:
  base:
    build:
      templates:
        - base
      args:
        - key: FOO
          value: bar
    run:
      args:
        - -it
        - --rm
      mounts:
        - /host:/container
  gpu:
    build:
      templates:
        - cuda
      args:
        - key: CUDA
          value: "12"
    run:
      args: []
      mounts: []
profiles:
  default:
    features:
      - base
  gpu-dev:
    features:
      - base
      - gpu
`

func TestListProfiles_ContainsAllProfiles(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	if err := os.WriteFile(paths.ConfigFile, []byte(multiProfileConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	out := config.ListProfiles(cfg)

	if !strings.Contains(out, "default") {
		t.Error("expected output to contain 'default'")
	}

	if !strings.Contains(out, "gpu-dev") {
		t.Error("expected output to contain 'gpu-dev'")
	}
}

func TestListProfiles_DefaultIsMarked(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	if err := os.WriteFile(paths.ConfigFile, []byte(multiProfileConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	out := config.ListProfiles(cfg)
	lines := strings.Split(strings.TrimSpace(out), "\n")

	var defaultLine string

	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "default") {
			defaultLine = l

			break
		}
	}

	if defaultLine == "" {
		t.Fatal("default profile line not found")
	}

	if !strings.Contains(defaultLine, "*") {
		t.Errorf("expected default profile line to contain '*', got: %q", defaultLine)
	}
}

func TestListProfiles_NonDefaultNotMarked(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	if err := os.WriteFile(paths.ConfigFile, []byte(multiProfileConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	out := config.ListProfiles(cfg)
	lines := strings.Split(strings.TrimSpace(out), "\n")

	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "gpu-dev") && strings.Contains(l, "*") {
			t.Errorf("non-default profile should not be marked with '*', got: %q", l)
		}
	}
}

func TestListFeatures_ContainsAllFeatures(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	if err := os.WriteFile(paths.ConfigFile, []byte(multiProfileConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	out := config.ListFeatures(cfg)

	if !strings.Contains(out, "base") {
		t.Error("expected output to contain 'base'")
	}

	if !strings.Contains(out, "gpu") {
		t.Error("expected output to contain 'gpu'")
	}
}

func TestListFeatures_ShowsCounts(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	if err := os.WriteFile(paths.ConfigFile, []byte(multiProfileConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	out := config.ListFeatures(cfg)

	if !strings.Contains(out, "templates=1") {
		t.Errorf("expected templates=1 in output, got: %q", out)
	}

	if !strings.Contains(out, "build_args=1") {
		t.Errorf("expected build_args=1 in output, got: %q", out)
	}
}

func TestSetDefaultProfile_ChangesDefault(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	if err := os.WriteFile(paths.ConfigFile, []byte(multiProfileConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := config.SetDefaultProfile(paths.ConfigFile, "gpu-dev"); err != nil {
		t.Fatalf("set default error: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load after set error: %v", err)
	}

	if cfg.Defaults.Profile != "gpu-dev" {
		t.Errorf("expected profile 'gpu-dev', got %q", cfg.Defaults.Profile)
	}
}

func TestSetDefaultProfile_UnknownProfileReturnsError(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	if err := os.WriteFile(paths.ConfigFile, []byte(multiProfileConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := config.SetDefaultProfile(paths.ConfigFile, "nonexistent"); err == nil {
		t.Error("expected error for unknown profile, got nil")
	}
}

func TestSetDefaultProfile_PreservesComments(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfgWithComment := `defaults:
  suffix: test
  # the active profile
  profile: default
features:
  base:
    build:
      templates: []
      args: []
    run:
      args: []
      mounts: []
profiles:
  default:
    features:
      - base
  other:
    features:
      - base
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfgWithComment), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := config.SetDefaultProfile(paths.ConfigFile, "other"); err != nil {
		t.Fatalf("set default error: %v", err)
	}

	out, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		t.Fatalf("read after set error: %v", err)
	}

	if !strings.Contains(string(out), "the active profile") {
		t.Error("expected comment to be preserved after rewrite")
	}
}

func TestSetDefaultProfile_PreservesKeyOrdering(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfgWithOrdering := `defaults:
  suffix: test
  profile: default
features:
  base:
    build:
      templates: []
      args: []
    run:
      args: []
      mounts: []
profiles:
  default:
    features:
      - base
  other:
    features:
      - base
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfgWithOrdering), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := config.SetDefaultProfile(paths.ConfigFile, "other"); err != nil {
		t.Fatalf("set default error: %v", err)
	}

	out, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		t.Fatalf("read after set error: %v", err)
	}

	content := string(out)
	defaultsIdx := strings.Index(content, "defaults:")
	featuresIdx := strings.Index(content, "features:")
	profilesIdx := strings.Index(content, "profiles:")

	if defaultsIdx == -1 || featuresIdx == -1 || profilesIdx == -1 {
		t.Fatal("expected all top-level keys to be present after rewrite")
	}

	if defaultsIdx >= featuresIdx || featuresIdx >= profilesIdx {
		t.Errorf("key ordering not preserved: defaults=%d features=%d profiles=%d", defaultsIdx, featuresIdx, profilesIdx)
	}
}

func TestShowResolved_ContainsAllProfiles(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	if err := os.WriteFile(paths.ConfigFile, []byte(multiProfileConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	out, err := config.ShowResolved(context.Background(), cfg)
	if err != nil {
		t.Fatalf("show resolved error: %v", err)
	}

	if !strings.Contains(out, "Profile: default") {
		t.Error("expected output to contain 'Profile: default'")
	}

	if !strings.Contains(out, "Profile: gpu-dev") {
		t.Error("expected output to contain 'Profile: gpu-dev'")
	}
}

func TestShowResolved_ContainsMergedBuildArgs(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	if err := os.WriteFile(paths.ConfigFile, []byte(multiProfileConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	out, err := config.ShowResolved(context.Background(), cfg)
	if err != nil {
		t.Fatalf("show resolved error: %v", err)
	}

	if !strings.Contains(out, "FOO=bar") {
		t.Errorf("expected FOO=bar in resolved output, got: %q", out)
	}

	if !strings.Contains(out, "CUDA=12") {
		t.Errorf("expected CUDA=12 in resolved output for gpu-dev, got: %q", out)
	}
}
