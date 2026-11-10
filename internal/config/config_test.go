package config_test

import (
	"context"
	goerrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/future-cyborg/ldocker/internal/config"
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

	tmpDir, err := os.MkdirTemp("", "ldocker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	t.Setenv("LDOCKER_CONFIG_DIR", tmpDir)

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

	resolved, err := conf.ResolveProfile(context.Background(), "default")
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

	resolved, err := conf.ResolveProfile(context.Background(), "combined")
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

	_, err = conf.ResolveProfile(context.Background(), "nonexistent")
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

	_, err = conf.ResolveProfile(context.Background(), "bad")
	if err == nil {
		t.Error("expected error for unknown feature, got nil")
	}
}

func TestResolveProfile_MountExpandsSubshell(t *testing.T) {
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
        - /etc/passwd:/etc/passwd:ro
        - ~/.bashrc:/home/$(id -un)/.bashrc:ro
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

	resolved, err := conf.ResolveProfile(context.Background(), "default")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Run.Mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d: %v", len(resolved.Run.Mounts), resolved.Run.Mounts)
	}

	if resolved.Run.Mounts[0] != "/etc/passwd:/etc/passwd:ro" {
		t.Errorf("unexpected first mount: %q", resolved.Run.Mounts[0])
	}

	if strings.Contains(resolved.Run.Mounts[1], "$(id -un)") {
		t.Errorf("expected subshell to be evaluated in mount, got: %q", resolved.Run.Mounts[1])
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

	resolved, err := conf.ResolveProfile(context.Background(), "default")
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

	resolved, err := conf.ResolveProfile(context.Background(), "default")
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

func TestResolveProfile_UndefinedEnvVarReturnsError(t *testing.T) {
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
        - $LDOCKER_DEFINITELY_UNSET_VAR_XYZ:/container
profiles:
  default:
    features:
      - base
`
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	os.Unsetenv("LDOCKER_DEFINITELY_UNSET_VAR_XYZ") //nolint:errcheck,tenv // intentional unset for test

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	_, err = conf.ResolveProfile(context.Background(), "default")
	if err == nil {
		t.Error("expected error for undefined env var in mount, got nil")
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

	resolved, err := conf.ResolveProfile(context.Background(), "default")
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

	resolved, err := conf.ResolveProfile(context.Background(), conf.Defaults.Profile)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	want := conf.Defaults.Suffix + "-" + conf.Defaults.Profile
	if resolved.Suffix != want {
		t.Errorf("expected suffix %q, got %q", want, resolved.Suffix)
	}
}

func configWithRunArgs(args string) string {
	return `defaults:
  suffix: test
  profile: default
features:
  base:
    build:
      templates: []
      args: []
    run:
      args:
` + args + `
      mounts: []
profiles:
  default:
    features:
      - base
`
}

func writeAndResolve(t *testing.T, cfg string) (*config.ResolvedConfig, error) {
	t.Helper()

	paths, _ := config.GetConfigPaths()

	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	resolved, err := conf.ResolveProfile(context.Background(), "default")
	if err != nil {
		return nil, fmt.Errorf("resolve profile: %w", err)
	}

	return resolved, nil
}

func TestExpandRunArgs_StaticArgPassedThrough(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	resolved, err := writeAndResolve(t, configWithRunArgs("        - \"--rm\""))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Run.Args) != 1 || resolved.Run.Args[0] != "--rm" {
		t.Errorf("expected [--rm], got %v", resolved.Run.Args)
	}
}

func TestExpandRunArgs_MultiWordArgIsSplit(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	resolved, err := writeAndResolve(t, configWithRunArgs("        - \"-e TERM=xterm-256color\""))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Run.Args) != 2 || resolved.Run.Args[0] != "-e" || resolved.Run.Args[1] != "TERM=xterm-256color" {
		t.Errorf("expected [-e TERM=xterm-256color], got %v", resolved.Run.Args)
	}
}

func TestExpandRunArgs_EnvVarIsExpanded(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	t.Setenv("LDOCKER_TEST_WORKDIR", "/some/path")

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	resolved, err := writeAndResolve(t, configWithRunArgs("        - \"--workdir=$LDOCKER_TEST_WORKDIR\""))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Run.Args) != 1 || resolved.Run.Args[0] != "--workdir=/some/path" {
		t.Errorf("expected [--workdir=/some/path], got %v", resolved.Run.Args)
	}
}

func TestExpandRunArgs_SubshellIsEvaluated(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	resolved, err := writeAndResolve(t, configWithRunArgs("        - \"-u $(id -u):$(id -g)\""))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Run.Args) != 2 || resolved.Run.Args[0] != "-u" {
		t.Fatalf("expected 2 args starting with -u, got %v", resolved.Run.Args)
	}

	if resolved.Run.Args[1] == "$(id -u):$(id -g)" {
		t.Error("expected subshell to be evaluated, but it was not")
	}
}

func TestExpandRunArgs_MultipleArgsPreserveOrder(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	args := "        - \"--rm\"\n        - \"-it\"\n        - \"-e TERM=xterm-256color\""
	resolved, err := writeAndResolve(t, configWithRunArgs(args))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	want := []string{"--rm", "-it", "-e", "TERM=xterm-256color"}
	if len(resolved.Run.Args) != len(want) {
		t.Fatalf("expected %v, got %v", want, resolved.Run.Args)
	}

	for i, w := range want {
		if resolved.Run.Args[i] != w {
			t.Errorf("arg[%d]: expected %q, got %q", i, w, resolved.Run.Args[i])
		}
	}
}

func TestExpandRunArgs_EmptyArgListReturnsNil(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	resolved, err := writeAndResolve(t, configWithRunArgs("        []"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Run.Args) != 0 {
		t.Errorf("expected empty args, got %v", resolved.Run.Args)
	}
}

func TestExpandRunArgs_FailedSubshellReturnsError(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	arg := configWithRunArgs("        - \"--arg=${LDOCKER_UNSET_VAR_XYZ?undefined variable}\"")
	resolved, err := writeAndResolve(t, arg)
	if err == nil {
		t.Errorf("expected error for failed subshell, got resolved: %v", resolved)
	}
}

func configWithCmdArg(suffix, profile, cmd string) string {
	return `defaults:
  suffix: ` + suffix + `
  profile: ` + profile + `
features:
  base:
    build:
      templates: []
      args:
        - key: MY_ARG
          cmd: ` + cmd + `
    run:
      args: []
      mounts: []
profiles:
  default:
    features:
      - base
`
}

func TestPopulateArg_BothValueAndCmdReturnsError(t *testing.T) {
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
          value: static
          cmd: echo dynamic
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
		// Schema rejects having both value and cmd set — acceptable error path.
		return
	}

	_, err = conf.ResolveProfile(context.Background(), "default")
	if err == nil {
		t.Error("expected error when both value and cmd are set, got nil")
	}
}

func TestPopulateArg_NeitherValueNorCmdReturnsError(t *testing.T) {
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
          value: ""
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

	_, err = conf.ResolveProfile(context.Background(), "default")
	if err == nil {
		t.Error("expected error when neither value nor cmd is set, got nil")
	}
}

func TestPopulateArg_CmdExitsNonZeroWithStderr(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfg := configWithCmdArg("test", "default", "sh -c 'echo oops >&2; exit 1'")
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	_, err = conf.ResolveProfile(context.Background(), "default")
	if err == nil {
		t.Error("expected error for non-zero exit with stderr, got nil")
	}
}

func TestPopulateArg_CmdExitsNonZeroWithoutStderr(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	paths, _ := config.GetConfigPaths()
	cfg := configWithCmdArg("test", "default", "sh -c 'exit 1'")
	if err := os.WriteFile(paths.ConfigFile, []byte(cfg), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	_, err = conf.ResolveProfile(context.Background(), "default")
	if err == nil {
		t.Error("expected error for non-zero exit without stderr, got nil")
	}
}

func TestExpandTilde_OnlyTilde(t *testing.T) {
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
        - "~"
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

	resolved, err := conf.ResolveProfile(context.Background(), "default")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	if len(resolved.Run.Mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(resolved.Run.Mounts))
	}

	if resolved.Run.Mounts[0] == "~" {
		t.Error("expected ~ to be expanded, but it was not")
	}
}

func TestGetLocalConfigPath_DefaultsToCwd(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("LDOCKER_LOCAL_CONFIG", "")

	path, explicit, err := config.GetLocalConfigPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, ".ldocker.yml")
	if path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, path)
	}

	if explicit {
		t.Error("expected explicit to be false, got true")
	}
}

func TestGetLocalConfigPath_UsesEnvOverride(t *testing.T) {
	envPath := "/some/explicit/path.yml"
	t.Setenv("LDOCKER_LOCAL_CONFIG", envPath)

	path, explicit, err := config.GetLocalConfigPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if path != envPath {
		t.Errorf("expected path %q, got %q", envPath, path)
	}

	if !explicit {
		t.Error("expected explicit to be true, got false")
	}
}

func TestLoadLocalConfig_NoFileReturnsEmptyConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("LDOCKER_LOCAL_CONFIG", "")

	local, err := config.LoadLocalConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if local == nil {
		t.Fatal("expected non-nil LocalConfig, got nil")
	}

	if len(local.Features) > 0 {
		t.Errorf("expected empty Features, got %v", local.Features)
	}

	if len(local.Profiles) > 0 {
		t.Errorf("expected empty Profiles, got %v", local.Profiles)
	}
}

func TestLoadLocalConfig_ExplicitEnvPathMissingReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	nonexistentPath := filepath.Join(tmpDir, "does-not-exist.yml")
	t.Setenv("LDOCKER_LOCAL_CONFIG", nonexistentPath)

	local, err := config.LoadLocalConfig()
	if err == nil {
		t.Fatalf("expected error for missing explicit path, got nil (local: %v)", local)
	}
}

func TestLoadLocalConfig_MalformedYAMLReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	malformedYAML := `features: [this is not: valid: yaml`
	if err := os.WriteFile(configPath, []byte(malformedYAML), 0o600); err != nil {
		t.Fatalf("failed to write malformed YAML: %v", err)
	}

	t.Setenv("LDOCKER_LOCAL_CONFIG", configPath)

	local, err := config.LoadLocalConfig()
	if err == nil {
		t.Fatalf("expected error for malformed YAML, got nil (local: %v)", local)
	}
}

func TestLoadLocalConfig_BuildKeyUnderFeatureReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	yamlWithBuild := `features:
  myfeature:
    build:
      templates: ["foo"]
    run:
      mounts: ["/a:/a"]
`
	if err := os.WriteFile(configPath, []byte(yamlWithBuild), 0o600); err != nil {
		t.Fatalf("failed to write YAML: %v", err)
	}

	t.Setenv("LDOCKER_LOCAL_CONFIG", configPath)

	local, err := config.LoadLocalConfig()
	if err == nil {
		t.Fatalf("expected error for build key in LocalFeature, got nil (local: %v)", local)
	}
}

func TestLoadLocalConfig_ParsesFeaturesAndProfiles(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	validYAML := `features:
  repos-team-a:
    run:
      mounts:
        - /host/a:/container/a
        - /host/b:/container/b
  repos-team-c:
    run:
      args: ["--env", "FOO=bar"]
profiles:
  agent-a:
    features: [base, repos-team-a]
`
	if err := os.WriteFile(configPath, []byte(validYAML), 0o600); err != nil {
		t.Fatalf("failed to write YAML: %v", err)
	}

	t.Setenv("LDOCKER_LOCAL_CONFIG", configPath)

	local, err := config.LoadLocalConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if local == nil {
		t.Fatal("expected non-nil LocalConfig")
	}

	// Check features
	if len(local.Features) != 2 {
		t.Errorf("expected 2 features, got %d", len(local.Features))
	}

	teamA, ok := local.Features["repos-team-a"]
	if !ok {
		t.Fatalf("expected repos-team-a feature")
	}

	if len(teamA.Run.Mounts) != 2 {
		t.Errorf("expected 2 mounts in repos-team-a, got %d: %v", len(teamA.Run.Mounts), teamA.Run.Mounts)
	}

	if teamA.Run.Mounts[0] != "/host/a:/container/a" {
		t.Errorf("expected first mount /host/a:/container/a, got %q", teamA.Run.Mounts[0])
	}

	if teamA.Run.Mounts[1] != "/host/b:/container/b" {
		t.Errorf("expected second mount /host/b:/container/b, got %q", teamA.Run.Mounts[1])
	}

	teamC, ok := local.Features["repos-team-c"]
	if !ok {
		t.Fatalf("expected repos-team-c feature")
	}

	if len(teamC.Run.Args) != 2 {
		t.Errorf("expected 2 args in repos-team-c, got %d: %v", len(teamC.Run.Args), teamC.Run.Args)
	}

	if teamC.Run.Args[0] != "--env" || teamC.Run.Args[1] != "FOO=bar" {
		t.Errorf("expected args [--env FOO=bar], got %v", teamC.Run.Args)
	}

	// Check profiles
	if len(local.Profiles) != 1 {
		t.Errorf("expected 1 profile, got %d", len(local.Profiles))
	}

	agentA, ok := local.Profiles["agent-a"]
	if !ok {
		t.Fatalf("expected agent-a profile")
	}

	if len(agentA.Features) != 2 {
		t.Errorf("expected 2 features in agent-a, got %d: %v", len(agentA.Features), agentA.Features)
	}

	if agentA.Features[0] != "base" || agentA.Features[1] != "repos-team-a" {
		t.Errorf("expected features [base repos-team-a], got %v", agentA.Features)
	}
}

func TestLoadLocalConfig_ParsesDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	validYAML := `defaults:
  profile: agent-a
profiles:
  agent-a:
    extends: default
    features: [repos-team-a]
`
	if err := os.WriteFile(configPath, []byte(validYAML), 0o600); err != nil {
		t.Fatalf("failed to write YAML: %v", err)
	}

	t.Setenv("LDOCKER_LOCAL_CONFIG", configPath)

	local, err := config.LoadLocalConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if local.Defaults.Profile != "agent-a" {
		t.Errorf("expected defaults.profile %q, got %q", "agent-a", local.Defaults.Profile)
	}

	if local.Profiles["agent-a"].Extends != "default" {
		t.Errorf("expected agent-a to extend %q, got %q", "default", local.Profiles["agent-a"].Extends)
	}
}

func TestMergeLocal_AddsNewFeaturesAndProfiles(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base": {
				Build: config.Build{Templates: []string{"base.dockerfile"}},
				Run:   config.Run{Args: []string{"--rm"}},
			},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	local := &config.LocalConfig{
		Features: map[string]config.LocalFeature{
			"local-feature": {
				Run: config.Run{Mounts: []string{"/local:/local"}},
			},
		},
		Profiles: map[string]config.Profile{
			"local-profile": {Features: []string{"base", "local-feature"}},
		},
	}

	prov, err := conf.MergeLocal(local)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if prov == nil {
		t.Fatal("expected non-nil LocalProvenance")
	}

	// Check that new feature was added
	if _, ok := conf.Features["local-feature"]; !ok {
		t.Errorf("expected local-feature to be added to conf.Features")
	}

	if conf.Features["local-feature"].Run.Mounts[0] != "/local:/local" {
		t.Errorf("expected local-feature mount to be set")
	}

	// Check that new profile was added
	if _, ok := conf.Profiles["local-profile"]; !ok {
		t.Errorf("expected local-profile to be added to conf.Profiles")
	}

	// Check provenance
	if !prov.Features["local-feature"] {
		t.Errorf("expected prov.Features[local-feature] to be true")
	}

	if !prov.Profiles["local-profile"] {
		t.Errorf("expected prov.Profiles[local-profile] to be true")
	}

	// Check that old items are NOT marked in provenance
	if prov.Features["base"] {
		t.Errorf("expected prov.Features[base] to be false")
	}

	if prov.Profiles["default"] {
		t.Errorf("expected prov.Profiles[default] to be false")
	}
}

func TestMergeLocal_FeatureCollisionReturnsError(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base": {
				Build: config.Build{Templates: []string{"base.dockerfile"}},
				Run:   config.Run{Args: []string{"--rm"}},
			},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	local := &config.LocalConfig{
		Features: map[string]config.LocalFeature{
			"base": { // collision with global feature
				Run: config.Run{Mounts: []string{"/local:/local"}},
			},
		},
		Profiles: map[string]config.Profile{},
	}

	prov, err := conf.MergeLocal(local)
	if err == nil {
		t.Fatalf("expected error for feature collision, got nil (prov: %v)", prov)
	}

	// Check that error wraps ErrLocalNameCollision
	if !goerrors.Is(err, config.ErrLocalNameCollision) {
		t.Errorf("expected error to wrap ErrLocalNameCollision")
	}
}

func TestMergeLocal_ProfileCollisionReturnsError(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base": {
				Build: config.Build{Templates: []string{"base.dockerfile"}},
				Run:   config.Run{Args: []string{"--rm"}},
			},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	local := &config.LocalConfig{
		Features: map[string]config.LocalFeature{},
		Profiles: map[string]config.Profile{
			"default": { // collision with global profile
				Features: []string{"base"},
			},
		},
	}

	prov, err := conf.MergeLocal(local)
	if err == nil {
		t.Fatalf("expected error for profile collision, got nil (prov: %v)", prov)
	}

	// Check that error wraps ErrLocalNameCollision
	if !goerrors.Is(err, config.ErrLocalNameCollision) {
		t.Errorf("expected error to wrap ErrLocalNameCollision")
	}
}

func TestMergeLocal_LocalFeatureBuildIsEmpty(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base": {
				Build: config.Build{Templates: []string{"base.dockerfile"}},
				Run:   config.Run{Args: []string{"--rm"}},
			},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	local := &config.LocalConfig{
		Features: map[string]config.LocalFeature{
			"local-feature": {
				Run: config.Run{Mounts: []string{"/local:/local"}},
			},
		},
		Profiles: map[string]config.Profile{},
	}

	prov, err := conf.MergeLocal(local)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	merged := conf.Features["local-feature"]
	if len(merged.Build.Templates) != 0 {
		t.Errorf("expected Build.Templates to be empty, got %v", merged.Build.Templates)
	}

	if len(merged.Build.Args) != 0 {
		t.Errorf("expected Build.Args to be empty, got %v", merged.Build.Args)
	}

	if !prov.Features["local-feature"] {
		t.Errorf("expected feature to be marked in provenance")
	}
}

func TestMergeLocal_EmptyLocalConfigIsNoop(t *testing.T) {
	originalFeatures := map[string]config.Feature{
		"base": {
			Build: config.Build{Templates: []string{"base.dockerfile"}},
			Run:   config.Run{Args: []string{"--rm"}},
		},
	}
	originalProfiles := map[string]config.Profile{
		"default": {Features: []string{"base"}},
	}

	conf := &config.Config{
		Features: originalFeatures,
		Profiles: originalProfiles,
	}

	empty := &config.LocalConfig{}

	prov, err := conf.MergeLocal(empty)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if prov == nil {
		t.Fatal("expected non-nil LocalProvenance")
	}

	// Check that Features and Profiles unchanged
	if len(conf.Features) != 1 || conf.Features["base"].Run.Args[0] != "--rm" {
		t.Errorf("expected Features to be unchanged")
	}

	if len(conf.Profiles) != 1 || conf.Profiles["default"].Features[0] != "base" {
		t.Errorf("expected Profiles to be unchanged")
	}

	// Check that provenance is empty but non-nil
	if len(prov.Features) != 0 {
		t.Errorf("expected empty prov.Features")
	}

	if len(prov.Profiles) != 0 {
		t.Errorf("expected empty prov.Profiles")
	}
}

func TestResolveFeatureRunOnly_ReturnsExpandedArgsAndMounts(t *testing.T) {
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
  myfeat:
    build:
      templates: []
      args: []
    run:
      args:
        - --rm
        - -it
        - -e HOME=$HOME
      mounts:
        - /host:/container
        - ~/.bashrc:/etc/bashrc:ro
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

	args, mounts, hasBuild, err := conf.ResolveFeatureRunOnly(context.Background(), "myfeat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hasBuild {
		t.Error("expected hasBuild to be false")
	}

	// Args are word-split by the shell expansion, so "-e HOME=$HOME" becomes ["-e", "HOME=/root"]
	if len(args) != 4 {
		t.Fatalf("expected 4 args (word-split), got %d: %v", len(args), args)
	}

	if args[0] != "--rm" || args[1] != "-it" || args[2] != "-e" {
		t.Errorf("expected first three args to be --rm, -it, and -e, got %v", args[:3])
	}

	// Fourth arg should have $HOME expanded
	if !strings.Contains(args[3], "HOME=") || strings.Contains(args[3], "$HOME") {
		t.Errorf("expected HOME=$HOME to be expanded in arg, got: %q", args[3])
	}

	if len(mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d: %v", len(mounts), mounts)
	}

	if mounts[0] != "/host:/container" {
		t.Errorf("expected first mount to be /host:/container, got %q", mounts[0])
	}

	// Second mount should have ~ expanded
	if mounts[1] == "~/.bashrc:/etc/bashrc:ro" || strings.Contains(mounts[1], "~") {
		t.Errorf("expected ~ to be expanded in mount, got: %q", mounts[1])
	}
}

func TestResolveFeatureRunOnly_UnknownFeatureReturnsError(t *testing.T) {
	cleanup := withTempConfigDir(t)
	defer cleanup()

	if err := config.InitConfig(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	conf, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	_, _, _, err = conf.ResolveFeatureRunOnly(context.Background(), "nonexistent-feature")
	if err == nil {
		t.Fatal("expected error for unknown feature, got nil")
	}

	// The error should mention the unknown feature error in its chain
	// (it's wrapped with the feature name, similar to ResolveProfile behavior)
	if !strings.Contains(err.Error(), "unknown feature") {
		t.Errorf("expected error to mention 'unknown feature', got: %v", err)
	}
}

func TestResolveFeatureRunOnly_HasBuildTrueWhenTemplatesPresent(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"build-feat": {
				Build: config.Build{
					Templates: []string{"base.dockerfile"},
					Args:      []config.BuildArg{},
				},
				Run: config.Run{
					Args:   []string{"--rm"},
					Mounts: []string{},
				},
			},
		},
	}

	args, mounts, hasBuild, err := conf.ResolveFeatureRunOnly(context.Background(), "build-feat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasBuild {
		t.Error("expected hasBuild to be true when Build.Templates is present")
	}

	if len(args) != 1 || args[0] != "--rm" {
		t.Errorf("expected args to contain only Run content, got %v", args)
	}

	if len(mounts) != 0 {
		t.Errorf("expected mounts to be empty, got %v", mounts)
	}
}

func TestResolveFeatureRunOnly_HasBuildTrueWhenArgsPresent(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"build-feat": {
				Build: config.Build{
					Templates: []string{},
					Args: []config.BuildArg{
						{Key: "VERSION", Value: "1.0"},
					},
				},
				Run: config.Run{
					Args:   []string{"-it"},
					Mounts: []string{},
				},
			},
		},
	}

	args, _, hasBuild, err := conf.ResolveFeatureRunOnly(context.Background(), "build-feat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasBuild {
		t.Error("expected hasBuild to be true when Build.Args is present")
	}

	if len(args) != 1 || args[0] != "-it" {
		t.Errorf("expected args to contain only Run content, got %v", args)
	}
}

func TestResolveFeatureRunOnly_HasBuildFalseWhenBuildEmpty(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"run-only": {
				Build: config.Build{
					Templates: []string{},
					Args:      []config.BuildArg{},
				},
				Run: config.Run{
					Args:   []string{"--rm"},
					Mounts: []string{"/data:/data"},
				},
			},
		},
	}

	args, mounts, hasBuild, err := conf.ResolveFeatureRunOnly(context.Background(), "run-only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hasBuild {
		t.Error("expected hasBuild to be false when Build content is empty")
	}

	if len(args) != 1 || args[0] != "--rm" {
		t.Errorf("expected args to contain only --rm, got %v", args)
	}

	if len(mounts) != 1 || mounts[0] != "/data:/data" {
		t.Errorf("expected mounts to contain only /data:/data, got %v", mounts)
	}
}

func TestResolveFeatureRunOnly_IgnoresBuildContent(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"mixed": {
				Build: config.Build{
					Templates: []string{"base.dockerfile", "app.dockerfile"},
					Args: []config.BuildArg{
						{Key: "VERSION", Value: "2.0"},
						{Key: "ENV", Value: "prod"},
					},
				},
				Run: config.Run{
					Args:   []string{"--rm", "-it"},
					Mounts: []string{"/app:/app"},
				},
			},
		},
	}

	args, mounts, hasBuild, err := conf.ResolveFeatureRunOnly(context.Background(), "mixed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasBuild {
		t.Error("expected hasBuild to be true")
	}

	// Verify only Run content is returned
	if len(args) != 2 || args[0] != "--rm" || args[1] != "-it" {
		t.Errorf("expected args to be [--rm -it], got %v", args)
	}

	if len(mounts) != 1 || mounts[0] != "/app:/app" {
		t.Errorf("expected mounts to be [/app:/app], got %v", mounts)
	}

	// Build content should never appear in the returned values
	// (structurally confirmed by the return signature not including Build)
}

func TestProfileChain_ExtendsAppendsOwnFeaturesAfterBase(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base":  {Run: config.Run{Args: []string{"-it"}}},
			"extra": {Run: config.Run{Args: []string{"--extra"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
			"agent-a": {Extends: "default", Features: []string{"extra"}},
		},
	}

	names, err := conf.ResolveFeatureNames("agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(names) != 2 || names[0] != "base" || names[1] != "extra" {
		t.Errorf("expected [base extra], got %v", names)
	}
}

func TestResolveProfile_ExtendsMergesBaseThenOwnFeatures(t *testing.T) {
	conf := &config.Config{
		Defaults: config.Defaults{Suffix: "test"},
		Features: map[string]config.Feature{
			"base":  {Run: config.Run{Args: []string{"-it"}}},
			"extra": {Run: config.Run{Args: []string{"--extra"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
			"agent-a": {Extends: "default", Features: []string{"extra"}},
		},
	}

	resolved, err := conf.ResolveProfile(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resolved.Run.Args) != 2 || resolved.Run.Args[0] != "-it" || resolved.Run.Args[1] != "--extra" {
		t.Errorf("expected run args [-it --extra], got %v", resolved.Run.Args)
	}
}

func TestProfileChain_UnknownExtendsTargetReturnsError(t *testing.T) {
	conf := &config.Config{
		Profiles: map[string]config.Profile{
			"agent-a": {Extends: "nonexistent", Features: []string{"base"}},
		},
	}

	_, err := conf.ResolveFeatureNames("agent-a")
	if err == nil {
		t.Fatal("expected error for unknown extends target, got nil")
	}
}

func TestProfileChain_CycleReturnsError(t *testing.T) {
	conf := &config.Config{
		Profiles: map[string]config.Profile{
			"a": {Extends: "b"},
			"b": {Extends: "a"},
		},
	}

	_, err := conf.ResolveFeatureNames("a")
	if err == nil {
		t.Fatal("expected error for extends cycle, got nil")
	}
}

func TestProfileChain_SelfExtendsReturnsError(t *testing.T) {
	conf := &config.Config{
		Profiles: map[string]config.Profile{
			"a": {Extends: "a"},
		},
	}

	_, err := conf.ResolveFeatureNames("a")
	if err == nil {
		t.Fatal("expected error for self-extending profile, got nil")
	}
}

func TestMergeLocal_LocalDefaultProfileOverridesGlobalDefault(t *testing.T) {
	conf := &config.Config{
		Defaults: config.Defaults{Profile: "default"},
		Features: map[string]config.Feature{
			"base": {Run: config.Run{Args: []string{"--rm"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	local := &config.LocalConfig{
		Defaults: config.LocalDefaults{Profile: "local-profile"},
		Profiles: map[string]config.Profile{
			"local-profile": {Features: []string{"base"}},
		},
	}

	prov, err := conf.MergeLocal(local)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conf.Defaults.Profile != "local-profile" {
		t.Errorf("expected conf.Defaults.Profile to be overridden to %q, got %q", "local-profile", conf.Defaults.Profile)
	}

	if !prov.DefaultFromLocal {
		t.Error("expected prov.DefaultFromLocal to be true")
	}
}

func TestMergeLocal_LocalDefaultProfileCanReferenceGlobalProfile(t *testing.T) {
	conf := &config.Config{
		Defaults: config.Defaults{Profile: "default"},
		Features: map[string]config.Feature{
			"base": {Run: config.Run{Args: []string{"--rm"}}},
			"gpu":  {Run: config.Run{Args: []string{"--gpus", "all"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
			"gpu-dev": {Features: []string{"base", "gpu"}},
		},
	}

	local := &config.LocalConfig{
		Defaults: config.LocalDefaults{Profile: "gpu-dev"},
	}

	prov, err := conf.MergeLocal(local)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conf.Defaults.Profile != "gpu-dev" {
		t.Errorf("expected conf.Defaults.Profile to be %q, got %q", "gpu-dev", conf.Defaults.Profile)
	}

	if !prov.DefaultFromLocal {
		t.Error("expected prov.DefaultFromLocal to be true")
	}
}

func TestMergeLocal_UnknownLocalDefaultProfileReturnsError(t *testing.T) {
	conf := &config.Config{
		Defaults: config.Defaults{Profile: "default"},
		Features: map[string]config.Feature{
			"base": {Run: config.Run{Args: []string{"--rm"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	local := &config.LocalConfig{
		Defaults: config.LocalDefaults{Profile: "nonexistent"},
	}

	_, err := conf.MergeLocal(local)
	if err == nil {
		t.Fatal("expected error for unknown local defaults.profile, got nil")
	}
}

func TestMergeLocal_NoLocalDefaultLeavesGlobalDefaultUnchanged(t *testing.T) {
	conf := &config.Config{
		Defaults: config.Defaults{Profile: "default"},
		Features: map[string]config.Feature{
			"base": {Run: config.Run{Args: []string{"--rm"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	local := &config.LocalConfig{}

	prov, err := conf.MergeLocal(local)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conf.Defaults.Profile != "default" {
		t.Errorf("expected conf.Defaults.Profile to remain %q, got %q", "default", conf.Defaults.Profile)
	}

	if prov.DefaultFromLocal {
		t.Error("expected prov.DefaultFromLocal to be false")
	}
}
