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

	if cfg.Defaults.Profile == "" {
		t.Error("expected default profile to be set")
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
  profile: default
`
	if err := os.WriteFile(paths.ConfigFile, []byte(invalidConfig), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, err := config.LoadConfig()
	if err == nil {
		t.Error("expected error for invalid config, got nil")
	}
}

func TestLoadConfig_DefaultProfileMissingReturnsError(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfg := `defaults:
  suffix: test
  profile: nonexistent
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
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, err := config.LoadConfig()
	if err == nil {
		t.Error("expected error when defaults.profile references unknown profile, got nil")
	}
}

// validConfig returns a minimal valid config with a named feature and profile.
func validConfig(suffix, profile, extraFeatures, extraProfiles string) string {
	return `defaults:
  suffix: ` + suffix + `
  profile: ` + profile + `
features:
  base:
    build:
      templates: []
      args: []
    run:
      args: []
      mounts: []
` + extraFeatures + `
profiles:
  default:
    features:
      - base
` + extraProfiles
}

func TestResolveProfile_BasicFeature(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfg := `defaults:
  suffix: test
  profile: default
features:
  base:
    build:
      templates:
        - mytemplate
      args:
        - key: FOO
          value: bar
    run:
      args:
        - -it
        - --rm
      mounts:
        - /host:/container
profiles:
  default:
    features:
      - base
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	resolved, err := conf.ResolveProfile("default")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Build.Templates) != 1 || resolved.Build.Templates[0] != "mytemplate" {
		t.Errorf("expected templates [mytemplate], got %v", resolved.Build.Templates)
	}

	if len(resolved.Build.Args) != 1 || resolved.Build.Args[0].Key != "FOO" || resolved.Build.Args[0].Value != "bar" {
		t.Errorf("unexpected build args: %v", resolved.Build.Args)
	}

	if len(resolved.Run.Args) != 2 || resolved.Run.Args[0] != "-it" {
		t.Errorf("unexpected run args: %v", resolved.Run.Args)
	}

	if len(resolved.Run.Mounts) != 1 || resolved.Run.Mounts[0] != "/host:/container" {
		t.Errorf("unexpected mounts: %v", resolved.Run.Mounts)
	}
}

func TestResolveProfile_MultipleFeaturesAreMergedInOrder(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfg := `defaults:
  suffix: test
  profile: combined
features:
  base:
    build:
      templates:
        - base
      args:
        - key: A
          value: "1"
    run:
      args:
        - --rm
      mounts:
        - /base:/base
  tool:
    build:
      templates:
        - tool
      args:
        - key: B
          value: "2"
    run:
      args:
        - -it
      mounts:
        - /tool:/tool
profiles:
  combined:
    features:
      - base
      - tool
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	resolved, err := conf.ResolveProfile("combined")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Build.Templates) != 2 ||
		resolved.Build.Templates[0] != "base" ||
		resolved.Build.Templates[1] != "tool" {
		t.Errorf("expected templates [base tool], got %v", resolved.Build.Templates)
	}

	if len(resolved.Build.Args) != 2 || resolved.Build.Args[0].Key != "A" || resolved.Build.Args[1].Key != "B" {
		t.Errorf("expected args [A B], got %v", resolved.Build.Args)
	}

	if len(resolved.Run.Args) != 2 || resolved.Run.Args[0] != "--rm" || resolved.Run.Args[1] != "-it" {
		t.Errorf("expected run args [--rm -it], got %v", resolved.Run.Args)
	}

	if len(resolved.Run.Mounts) != 2 ||
		resolved.Run.Mounts[0] != "/base:/base" ||
		resolved.Run.Mounts[1] != "/tool:/tool" {
		t.Errorf("expected mounts [/base:/base /tool:/tool], got %v", resolved.Run.Mounts)
	}
}

func TestResolveProfile_UnknownProfileReturnsError(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	_, err = conf.ResolveProfile("nonexistent")
	if err == nil {
		t.Error("expected error for unknown profile, got nil")
	}
}

func TestResolveProfile_UnknownFeatureReturnsError(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfg := `defaults:
  suffix: test
  profile: bad
features:
  base:
    build:
      templates: []
      args: []
    run:
      args: []
      mounts: []
profiles:
  bad:
    features:
      - nonexistent-feature
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	_, err = conf.ResolveProfile("bad")
	if err == nil {
		t.Error("expected error for unknown feature, got nil")
	}
}

func TestResolveProfile_MountExpandsTilde(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfg := `defaults:
  suffix: test
  profile: default
features:
  base:
    build:
      templates: []
      args: []
    run:
      args: []
      mounts:
        - ~/mydir:/container
profiles:
  default:
    features:
      - base
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	resolved, err := conf.ResolveProfile("default")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Run.Mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(resolved.Run.Mounts))
	}

	if resolved.Run.Mounts[0] == "~/mydir:/container" {
		t.Error("expected ~ to be expanded, but it was not")
	}
}

func TestResolveProfile_MountExpandsEnvVar(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	t.Setenv("MY_HOST_DIR", "/expanded/path")

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfg := `defaults:
  suffix: test
  profile: default
features:
  base:
    build:
      templates: []
      args: []
    run:
      args: []
      mounts:
        - $MY_HOST_DIR:/container
profiles:
  default:
    features:
      - base
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	resolved, err := conf.ResolveProfile("default")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Run.Mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(resolved.Run.Mounts))
	}

	if resolved.Run.Mounts[0] != "/expanded/path:/container" {
		t.Errorf("expected expanded mount, got %q", resolved.Run.Mounts[0])
	}
}

func TestResolveProfile_EvaluatesCmdArg(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfg := `defaults:
  suffix: test
  profile: default
features:
  base:
    build:
      templates: []
      args:
        - key: MY_ARG
          cmd: echo hello
    run:
      args: []
      mounts: []
profiles:
  default:
    features:
      - base
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	resolved, err := conf.ResolveProfile("default")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Build.Args) != 1 {
		t.Fatalf("expected 1 build arg, got %d", len(resolved.Build.Args))
	}

	if resolved.Build.Args[0].Value != "hello" {
		t.Errorf("expected value 'hello', got '%s'", resolved.Build.Args[0].Value)
	}
}

func TestResolveProfile_ResolvedSuffixIncludesProfileName(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	resolved, err := conf.ResolveProfile(conf.Defaults.Profile)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	want := conf.Defaults.Suffix + "-" + conf.Defaults.Profile
	if resolved.Suffix != want {
		t.Errorf("expected suffix %q, got %q", want, resolved.Suffix)
	}
}
