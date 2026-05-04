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
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/the4thamigo-uk/conflate"

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
	errUndefinedEnvVar   = goerrors.New("undefined environment variable in mount")
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
		currentUser, err := user.Current()
		if err != nil {
			return Paths{}, errors.Wrap(err)
		}

		configDir = filepath.Join(currentUser.HomeDir, ".config", "udocker")
	}

	return Paths{
		ConfigDir:    configDir,
		ConfigFile:   filepath.Join(configDir, "config.yml"),
		TemplatesDir: filepath.Join(configDir, "templates"),
	}, nil
}

// ResolveProfile merges the named profile's features in order and evaluates
// cmd-type build args. Use ResolveProfileNoArgs when build args are not needed.
func (conf *Config) ResolveProfile(name string) (*ResolvedConfig, error) {
	resolved, err := conf.mergeProfile(name)
	if err != nil {
		return nil, err
	}

	if err = resolved.populateArgs(); err != nil {
		return nil, errors.Wrap(err)
	}

	return resolved, nil
}

// ResolveProfileNoArgs merges the named profile's features without evaluating
// cmd-type build args. Use this for the run command.
func (conf *Config) ResolveProfileNoArgs(name string) (*ResolvedConfig, error) {
	return conf.mergeProfile(name)
}

func (conf *Config) mergeProfile(name string) (*ResolvedConfig, error) {
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

		mounts, err := expandMounts(feature.Run.Mounts)
		if err != nil {
			return nil, errors.Wrap(err)
		}

		resolved.Run.Mounts = append(resolved.Run.Mounts, mounts...)
	}

	return resolved, nil
}

func expandMounts(mounts []string) ([]string, error) {
	if len(mounts) == 0 {
		return nil, nil
	}

	expanded := make([]string, len(mounts))

	for i, m := range mounts {
		var err error

		m, err = expandEnvStrict(m)
		if err != nil {
			return nil, err
		}

		m, err = expandTilde(m)
		if err != nil {
			return nil, err
		}

		expanded[i] = m
	}

	return expanded, nil
}

func expandEnvStrict(s string) (string, error) {
	var missingVar string

	result := os.Expand(s, func(key string) string {
		val, ok := os.LookupEnv(key)
		if !ok {
			missingVar = key

			return ""
		}

		return val
	})

	if missingVar != "" {
		return "", errors.Wrap(fmt.Errorf("%w: $%s", errUndefinedEnvVar, missingVar))
	}

	return result, nil
}

func expandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") && path != "~" {
		return path, nil
	}

	currentUser, err := user.Current()
	if err != nil {
		return "", errors.Wrap(err)
	}

	return currentUser.HomeDir + path[1:], nil
}

func (resolved *ResolvedConfig) populateArgs() error {
	for i := range resolved.Build.Args {
		arg := &resolved.Build.Args[i]

		value, err := populateArg(arg.Value, arg.Cmd, arg)
		if err != nil {
			return err
		}

		arg.Value = value
	}

	return nil
}

func populateArg(value, cmd string, buildArg *BuildArg) (string, error) {
	if value != "" && cmd != "" {
		return "", errors.Wrap(errBothValueAndCmd, buildArg)
	}

	if value == "" && cmd == "" {
		return "", errors.Wrap(errMissingValueOrCmd, buildArg)
	}

	if value != "" {
		return value, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
