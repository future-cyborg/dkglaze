//nolint:revive // max-public-structs: config subcommands intentionally exceed per-file limit
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/future-cyborg/dkglaze/internal/config"
	"github.com/future-cyborg/dkglaze/internal/errors"
	"github.com/future-cyborg/dkglaze/internal/logger"
)

// ConfigInitCmd initializes the config directory and default config file.
type ConfigInitCmd struct{}

func (*ConfigInitCmd) Run(_ *Context) error {
	return errors.Wrap(config.InitConfig())
}

// ConfigListProfilesCmd lists all configured profiles.
type ConfigListProfilesCmd struct{}

func (*ConfigListProfilesCmd) Run(_ *Context) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return errors.Wrap(err)
	}

	fmt.Print(config.ListProfiles(cfg))

	return nil
}

// ConfigListFeaturesCmd lists all configured features.
type ConfigListFeaturesCmd struct{}

func (*ConfigListFeaturesCmd) Run(_ *Context) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return errors.Wrap(err)
	}

	fmt.Print(config.ListFeatures(cfg))

	return nil
}

// ConfigListCmd groups list subcommands.
type ConfigListCmd struct {
	Profiles ConfigListProfilesCmd `cmd:"" help:"List all profiles"`
	Features ConfigListFeaturesCmd `cmd:"" help:"List all features"`
}

// ConfigSetDefaultCmd changes the default profile in the config file.
type ConfigSetDefaultCmd struct {
	Profile string `arg:"" name:"profile" help:"Profile to set as default"`
}

func (cmd *ConfigSetDefaultCmd) Run(_ *Context) error {
	paths, err := config.GetConfigPaths()
	if err != nil {
		return errors.Wrap(err)
	}

	if err := config.SetDefaultProfile(paths.ConfigFile, cmd.Profile); err != nil {
		return errors.Wrap(err)
	}

	logger.PrintColorF(logger.Green, "default profile set to %q", cmd.Profile)

	return nil
}

// ConfigSetCmd groups set subcommands.
type ConfigSetCmd struct {
	Default ConfigSetDefaultCmd `cmd:"" help:"Set the default profile"`
}

// ConfigValidateCmd validates the config file.
type ConfigValidateCmd struct{}

func (*ConfigValidateCmd) Run(_ *Context) error {
	if _, err := config.LoadConfig(); err != nil {
		return errors.Wrap(err)
	}

	logger.PrintColor(logger.Green, "config is valid")

	return nil
}

// ConfigShowCmd prints the config file or resolved config per profile.
type ConfigShowCmd struct {
	Resolved bool `name:"resolved" help:"Show computed values after merging features and expanding variables"`
}

func (cmd *ConfigShowCmd) Run(ctx *Context) error {
	if cmd.Resolved {
		return showResolved(ctx.Ctx)
	}

	return showRaw()
}

func showResolved(ctx context.Context) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return errors.Wrap(err)
	}

	out, err := config.ShowResolved(ctx, cfg)
	if err != nil {
		return errors.Wrap(err)
	}

	fmt.Print(out)

	return nil
}

func showRaw() error {
	paths, err := config.GetConfigPaths()
	if err != nil {
		return errors.Wrap(err)
	}

	data, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		return errors.Wrap(err)
	}

	fmt.Print(string(data))

	return nil
}

// ConfigEditCmd opens the config file in $EDITOR.
type ConfigEditCmd struct{}

func (*ConfigEditCmd) Run(ctx *Context) error {
	paths, err := config.GetConfigPaths()
	if err != nil {
		return errors.Wrap(err)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	//nolint:gosec // editor and path are from user environment / config
	c := exec.CommandContext(ctx.Ctx, editor, paths.ConfigFile)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return errors.Wrap(c.Run())
}

// ConfigPathCmd prints the config file path.
type ConfigPathCmd struct{}

func (*ConfigPathCmd) Run(_ *Context) error {
	paths, err := config.GetConfigPaths()
	if err != nil {
		return errors.Wrap(err)
	}

	fmt.Println(paths.ConfigFile)

	return nil
}

// ConfigCmd groups all config subcommands.
type ConfigCmd struct {
	Init     ConfigInitCmd     `cmd:"" help:"Create the config directory and default config.yml"`
	List     ConfigListCmd     `cmd:"" help:"List profiles or features"`
	Set      ConfigSetCmd      `cmd:"" help:"Update config values"`
	Validate ConfigValidateCmd `cmd:"" help:"Validate the config file"`
	Show     ConfigShowCmd     `cmd:"" help:"Print the config file, or --resolved to show computed values"`
	Edit     ConfigEditCmd     `cmd:"" help:"Open the config file in $EDITOR"`
	Path     ConfigPathCmd     `cmd:"" help:"Print the config file path"`
}
