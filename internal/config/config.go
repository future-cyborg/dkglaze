// Package config provides configuration utilities.
package config

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
)

// Config represents the configuration.
type Config struct {
	Defaults Defaults `yaml:"defaults"`
	Build    Build    `yaml:"build"`
	Run      Run      `yaml:"run"`
}

// Defaults represents configuration for the default values.
type Defaults struct {
	Suffix string `yaml:"suffix"`
}

// Build represents configuration for the build command.
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

// Run represents configuration for the run command.
type Run struct {
	Args []string `yaml:"args"`
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

// LoadConfig loads the config file and evaluates all cmd-type build args.
func LoadConfig() (*Config, error) {
	cfg, err := unmarshalConfig()
	if err != nil {
		return nil, err
	}

	if err = cfg.populateArgs(); err != nil {
		return nil, errors.Wrap(err)
	}

	return cfg, nil
}

// LoadConfigNoArgs loads the config file without evaluating cmd-type build args.
// Use this when build args are not needed (e.g. the run command).
func LoadConfigNoArgs() (*Config, error) {
	return unmarshalConfig()
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

func (conf *Config) populateArgs() error {
	for i := range conf.Build.Args {
		if err := populateArg(&conf.Build.Args[i]); err != nil {
			return err
		}
	}

	return nil
}

func populateArg(buildArg *BuildArg) error {
	if buildArg.Value != "" && buildArg.Cmd != "" {
		return errors.Wrap(errBothValueAndCmd, buildArg)
	}

	if buildArg.Value == "" && buildArg.Cmd == "" {
		return errors.Wrap(errMissingValueOrCmd, buildArg)
	}

	if buildArg.Value != "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	//nolint:gosec // command is sourced from user's config file
	cmd := exec.CommandContext(ctx, "sh", "-c", buildArg.Cmd)
	cmd.Env = os.Environ()

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	output, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return errors.Wrap(fmt.Errorf("%w: %s", errRunArgCommand, msg), buildArg)
		}

		return errors.Wrap(errRunArgCommand, buildArg)
	}

	buildArg.Value = strings.TrimSpace(string(output))

	return nil
}
