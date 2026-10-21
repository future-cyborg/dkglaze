// Package config provides configuration utilities.
package config //nolint:revive // max-public-structs: this package intentionally exports all config types

import (
	"bytes"
	"context"
	_ "embed"
	goerrors "errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/the4thamigo-uk/conflate"
	"gopkg.in/yaml.v3"

	"github.com/future-cyborg/udocker/internal/errors"
	"github.com/future-cyborg/udocker/internal/logger"
)

//go:embed schema.json
var schema []byte

//go:embed default.yml
var defaultConfig []byte

var (
	errRunArgCommand     = goerrors.New("failed to run command for arg")
	errBothValueAndCmd   = goerrors.New("arg cannot have both value and cmd set")
	errMissingValueOrCmd = goerrors.New("arg must have either value or cmd set")
	errUnknownProfile    = goerrors.New("unknown profile")
	errUnknownFeature    = goerrors.New("unknown feature")
	errDefaultProfile    = goerrors.New("defaults.profile references unknown profile")

	// ErrLocalNameCollision is returned when a local config name collides with a global one.
	ErrLocalNameCollision = goerrors.New("name already defined in global config")
)

// Config represents the configuration.
type Config struct {
	Defaults Defaults           `yaml:"defaults"`
	Features map[string]Feature `yaml:"features"`
	Profiles map[string]Profile `yaml:"profiles"`
}

// Defaults represents configuration for the default values.
type Defaults struct {
	Suffix  string `yaml:"suffix"`
	Profile string `yaml:"profile"`
}

// Feature represents a named bundle of build and run configuration.
type Feature struct {
	Build Build `yaml:"build"`
	Run   Run   `yaml:"run"`
}

// Profile represents a named combination of features.
type Profile struct {
	Features []string `yaml:"features"`
}

// LocalFeature represents a named run-only bundle that may be defined in the
// project-local config file. It intentionally has no Build field: local
// features cannot contribute build templates or build args.
type LocalFeature struct {
	Run Run `yaml:"run"`
}

// LocalConfig represents the project-local config file ($PWD/.udocker.yml).
// It is parsed independently of the global Config — not validated against
// schema.json and not merged via conflate.
type LocalConfig struct {
	Features map[string]LocalFeature `yaml:"features"`
	Profiles map[string]Profile      `yaml:"profiles"`
}

// LocalProvenance records which feature/profile names in a merged Config
// originated from the local project-tree config file.
type LocalProvenance struct {
	Features map[string]bool
	Profiles map[string]bool
}

// Build represents build configuration.
type Build struct {
	Templates []string   `yaml:"templates"`
	Args      []BuildArg `yaml:"args"`
}

// BuildArg represents a build argument.
type BuildArg struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
	Cmd   string `yaml:"cmd"`
}

// Run represents run configuration.
type Run struct {
	Args   []string `yaml:"args"`
	Mounts []string `yaml:"mounts"`
}

// ResolvedConfig is the flat, merged result of applying a profile.
type ResolvedConfig struct {
	Suffix string
	Build  Build
	Run    Run
}

// Paths represents the config files and directories.
type Paths struct {
	ConfigDir    string
	ConfigFile   string
	TemplatesDir string
}

// InitConfig creates the config file if it doesn't exist.
func InitConfig() error {
	configFiles, err := GetConfigPaths()
	if err != nil {
		return errors.Wrap(err)
	}

	err = os.MkdirAll(configFiles.ConfigDir, 0o750)
	if err != nil {
		return errors.Wrap(err)
	}

	// if exists print message
	_, err = os.Stat(configFiles.ConfigFile)
	if err == nil {
		logger.PrintColorF(logger.Yellow, "config file already exists at %s", configFiles.ConfigFile)
	} else {
		err = os.WriteFile(configFiles.ConfigFile, defaultConfig, 0o600)
		if err != nil {
			return errors.Wrap(err)
		}

		logger.PrintColorF(logger.Green, "config file created at %s", configFiles.ConfigFile)
		logger.PrintColor(logger.Yellow, "this file has blank values by default. It must be edited to be useful")
	}

	err = os.MkdirAll(configFiles.TemplatesDir, 0o750)
	if err != nil {
		return errors.Wrap(err)
	}

	return nil
}

// LoadConfig loads and validates the config file.
func LoadConfig() (*Config, error) {
	cfg, err := unmarshalConfig()
	if err != nil {
		return nil, err
	}

	if _, ok := cfg.Profiles[cfg.Defaults.Profile]; !ok {
		return nil, errors.Wrap(fmt.Errorf("%w: %q", errDefaultProfile, cfg.Defaults.Profile))
	}

	return cfg, nil
}

func unmarshalConfig() (*Config, error) {
	configFiles, err := GetConfigPaths()
	if err != nil {
		return nil, errors.Wrap(err)
	}

	c, err := loadAndValidate(configFiles.ConfigFile)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	var cfg Config

	if err = c.Unmarshal(&cfg); err != nil {
		return nil, errors.Wrap(err)
	}

	return &cfg, nil
}

func loadAndValidate(configFile string) (*conflate.Conflate, error) {
	c, err := conflate.FromFiles(configFile)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	s, err := conflate.NewSchemaData(schema)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	err = c.Validate(s)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return c, nil
}

// GetConfigPaths returns the config files and directories.
func GetConfigPaths() (Paths, error) {
	configDir := os.Getenv("UDOCKER_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, errors.Wrap(err)
		}

		configDir = filepath.Join(home, ".config", "udocker")
	}

	return Paths{
		ConfigDir:    configDir,
		ConfigFile:   filepath.Join(configDir, "config.yml"),
		TemplatesDir: filepath.Join(configDir, "templates"),
	}, nil
}

// GetLocalConfigPath returns the path to the project-local config file and whether
// the path was explicitly set via the UDOCKER_LOCAL_CONFIG environment variable.
// If the env var is set, the path is explicit and a missing file is an error.
// If the env var is not set, the path defaults to $PWD/.udocker.yml and a missing
// file is a silent no-op (not an error).
func GetLocalConfigPath() (path string, explicit bool, err error) {
	envPath := os.Getenv("UDOCKER_LOCAL_CONFIG")
	if envPath != "" {
		return envPath, true, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", false, errors.Wrap(err)
	}

	return filepath.Join(cwd, ".udocker.yml"), false, nil
}

// LoadLocalConfig loads the project-local config file. It is parsed independently
// of the global Config — not validated against schema.json and not merged via
// conflate. Strict YAML decoding rejects unknown fields.
func LoadLocalConfig() (*LocalConfig, error) {
	path, explicit, err := GetLocalConfigPath()
	if err != nil {
		return nil, errors.Wrap(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is from env var or cwd, user-controlled
	if err != nil {
		if goerrors.Is(err, os.ErrNotExist) && !explicit {
			// Default path (.udocker.yml in cwd) not found is not an error.
			return &LocalConfig{}, nil
		}

		return nil, errors.Wrap(err)
	}

	var local LocalConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&local); err != nil {
		return nil, errors.Wrap(err)
	}

	return &local, nil
}

// MergeLocal merges local into conf, erroring if any local feature or
// profile name collides with an existing global one. It returns a
// LocalProvenance recording which names came from local.
func (conf *Config) MergeLocal(local *LocalConfig) (*LocalProvenance, error) {
	prov := &LocalProvenance{Features: map[string]bool{}, Profiles: map[string]bool{}}
	for name, lf := range local.Features {
		if _, exists := conf.Features[name]; exists {
			return nil, errors.Wrap(fmt.Errorf("%w: feature %q", ErrLocalNameCollision, name))
		}
		if conf.Features == nil {
			conf.Features = map[string]Feature{}
		}
		conf.Features[name] = Feature{Run: lf.Run}
		prov.Features[name] = true
	}
	for name, lp := range local.Profiles {
		if _, exists := conf.Profiles[name]; exists {
			return nil, errors.Wrap(fmt.Errorf("%w: profile %q", ErrLocalNameCollision, name))
		}
		if conf.Profiles == nil {
			conf.Profiles = map[string]Profile{}
		}
		conf.Profiles[name] = lp
		prov.Profiles[name] = true
	}

	return prov, nil
}

// ResolveProfile merges the named profile's features in order and evaluates
// cmd-type build args. Use ResolveProfileNoArgs when build args are not needed.
func (conf *Config) ResolveProfile(ctx context.Context, name string) (*ResolvedConfig, error) {
	resolved, err := conf.mergeProfile(ctx, name)
	if err != nil {
		return nil, err
	}

	if err = resolved.populateArgs(ctx); err != nil {
		return nil, errors.Wrap(err)
	}

	if err = resolved.expandRunArgs(ctx); err != nil {
		return nil, errors.Wrap(err)
	}

	return resolved, nil
}

// ResolveProfileNoArgs merges the named profile's features without evaluating
// cmd-type build args. Use this for the run command.
func (conf *Config) ResolveProfileNoArgs(ctx context.Context, name string) (*ResolvedConfig, error) {
	resolved, err := conf.mergeProfile(ctx, name)
	if err != nil {
		return nil, err
	}

	if err = resolved.expandRunArgs(ctx); err != nil {
		return nil, errors.Wrap(err)
	}

	return resolved, nil
}

// ResolveFeatureRunOnly looks up a named feature in conf.Features and returns its
// expanded Run.Args and Run.Mounts. Build content is ignored by the caller;
// hasBuild reports whether the feature had non-empty Build content, so callers
// can warn. Returns errUnknownFeature if the feature name is not found.
//
//nolint:revive,lll // function-result-limit: args/mounts/hasBuild/err are clearer as separate return values
func (conf *Config) ResolveFeatureRunOnly(ctx context.Context, name string) (args, mounts []string, hasBuild bool, err error) {
	feature, ok := conf.Features[name]
	if !ok {
		return nil, nil, false, errors.Wrap(fmt.Errorf("%w: %s", errUnknownFeature, name))
	}

	hasBuild = len(feature.Build.Templates) > 0 || len(feature.Build.Args) > 0

	expandedArgs, err := expandRunArgList(ctx, feature.Run.Args)
	if err != nil {
		return nil, nil, hasBuild, errors.Wrap(err)
	}

	expandedMounts, err := expandMounts(ctx, feature.Run.Mounts)
	if err != nil {
		return nil, nil, hasBuild, errors.Wrap(err)
	}

	return expandedArgs, expandedMounts, hasBuild, nil
}

func (conf *Config) mergeProfile(ctx context.Context, name string) (*ResolvedConfig, error) {
	profile, ok := conf.Profiles[name]
	if !ok {
		return nil, errors.Wrap(fmt.Errorf("%w: %s", errUnknownProfile, name))
	}

	resolved := &ResolvedConfig{Suffix: conf.Defaults.Suffix + "-" + name}

	for _, featureName := range profile.Features {
		feature, ok := conf.Features[featureName]
		if !ok {
			return nil, errors.Wrap(fmt.Errorf("%w: %s", errUnknownFeature, featureName))
		}

		resolved.Build.Templates = append(resolved.Build.Templates, feature.Build.Templates...)
		resolved.Build.Args = append(resolved.Build.Args, feature.Build.Args...)
		resolved.Run.Args = append(resolved.Run.Args, feature.Run.Args...)

		mounts, err := expandMounts(ctx, feature.Run.Mounts)
		if err != nil {
			return nil, errors.Wrap(err)
		}

		resolved.Run.Mounts = append(resolved.Run.Mounts, mounts...)
	}

	return resolved, nil
}

func expandMounts(ctx context.Context, mounts []string) ([]string, error) {
	var result []string

	for _, m := range mounts {
		parts, err := expandMountString(ctx, m)
		if err != nil {
			return nil, err
		}

		result = append(result, parts...)
	}

	return result, nil
}

func (resolved *ResolvedConfig) populateArgs(ctx context.Context) error {
	for i := range resolved.Build.Args {
		arg := &resolved.Build.Args[i]

		value, err := populateArg(ctx, arg.Value, arg.Cmd, arg)
		if err != nil {
			return err
		}

		arg.Value = value
	}

	return nil
}

func populateArg(ctx context.Context, value, cmd string, buildArg *BuildArg) (string, error) {
	if value != "" && cmd != "" {
		return "", errors.Wrap(errBothValueAndCmd, buildArg)
	}

	if value == "" && cmd == "" {
		return "", errors.Wrap(errMissingValueOrCmd, buildArg)
	}

	if value != "" {
		return value, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	//nolint:gosec // command is sourced from user's config file
	execCmd := exec.CommandContext(ctx, "sh", "-c", cmd)
	execCmd.Env = os.Environ()

	var stderr bytes.Buffer

	execCmd.Stderr = &stderr

	output, err := execCmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", errors.Wrap(fmt.Errorf("%w: %s", errRunArgCommand, msg), buildArg)
		}

		return "", errors.Wrap(errRunArgCommand, buildArg)
	}

	return strings.TrimSpace(string(output)), nil
}

func (resolved *ResolvedConfig) expandRunArgs(ctx context.Context) error {
	expanded, err := expandRunArgList(ctx, resolved.Run.Args)
	if err != nil {
		return err
	}

	resolved.Run.Args = expanded

	return nil
}

func expandRunArgList(ctx context.Context, args []string) ([]string, error) {
	var result []string

	for _, arg := range args {
		parts, err := shellExpandWordSplit(ctx, arg)
		if err != nil {
			return nil, err
		}

		result = append(result, parts...)
	}

	return result, nil
}

func expandMountString(ctx context.Context, s string) ([]string, error) {
	return shellExpand(ctx, fmt.Sprintf("set -eu; __v=%s; printf '%%s\\n' \"$__v\"", s))
}

func shellExpandWordSplit(ctx context.Context, s string) ([]string, error) {
	return shellExpand(ctx, fmt.Sprintf("set -eu; printf '%%s\\n' %s", s))
}

// shellExpand runs script through bash and returns the newline-separated output
// as a slice. The caller is responsible for constructing the script with the
// appropriate quoting for word-split vs no-split behavior.
func shellExpand(ctx context.Context, script string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	//nolint:gosec // command is sourced from user's config file
	execCmd := exec.CommandContext(ctx, "bash", "-c", script)
	execCmd.Env = os.Environ()

	var stderr bytes.Buffer

	execCmd.Stderr = &stderr

	output, err := execCmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, errors.Wrap(fmt.Errorf("%w: %s", errRunArgCommand, msg))
		}

		return nil, errors.Wrap(errRunArgCommand)
	}

	var parts []string

	for _, p := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if p != "" {
			parts = append(parts, p)
		}
	}

	return parts, nil
}
