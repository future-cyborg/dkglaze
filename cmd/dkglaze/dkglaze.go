// Package main is the entry point for the dkglaze CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"syscall"

	"github.com/alecthomas/kong"

	"github.com/future-cyborg/dkglaze/internal/config"
	"github.com/future-cyborg/dkglaze/internal/docker"
	"github.com/future-cyborg/dkglaze/internal/errors"
	"github.com/future-cyborg/dkglaze/internal/logger"
)

// Context holds global CLI context.
type Context struct {
	Ctx     context.Context
	Verbose bool
}

// BuildCmd is the build subcommand.
type BuildCmd struct {
	Image           string   `arg:"" name:"image" help:"Image to build FROM" completion:"images"`
	BuildArgs       []string `arg:"" optional:"" name:"build-args" help:"Build arguments passed to docker build."`
	Profile         string   `short:"p" name:"profile" completion:"profiles" help:"Profile to activate (overrides defaults.profile)."` //nolint:revive,lll // keep descriptive help text
	Pull            bool     `name:"pull" help:"Pull the latest base image before building."`
	DryRun          bool     `short:"n" help:"Dry run. Print commands without executing."`
	TrustLocalShell bool     `name:"trust" help:"Allow run.args/run.mounts from project-local .dkglaze.yml to be shell-expanded. Can execute arbitrary commands — only use for a trusted .dkglaze.yml."` //nolint:revive,lll // keep descriptive help text
}

func (*BuildCmd) Help() string {
	return "Use '--' before build-args, so args prefixed with '-' get passed to docker build."
}

func (cmd *BuildCmd) Run(ctx *Context) error {
	return errors.Wrap(docker.Build(ctx.Ctx, &docker.BuildOptions{
		Image:           cmd.Image,
		BuildArgs:       cmd.BuildArgs,
		Profile:         cmd.Profile,
		Pull:            cmd.Pull,
		DryRun:          cmd.DryRun,
		TrustLocalShell: cmd.TrustLocalShell,
	}))
}

// RunCmd is the run subcommand.
type RunCmd struct {
	Image           string   `arg:"" name:"image" help:"Image to run user-layer on top of" completion:"images"`
	RunArgs         []string `arg:"" optional:"" name:"args" help:"Arguments passed to docker run"`
	Profile         string   `short:"p" name:"profile" completion:"profiles" help:"Profile to activate (overrides defaults.profile)."`                                                                  //nolint:revive,lll // keep descriptive help text
	Features        []string `short:"f" name:"feature" completion:"features" help:"Add named feature(s) from config or ./.dkglaze.yml (run only; build config on the feature is ignored). Repeatable."` //nolint:revive,lll // keep descriptive help text
	DryRun          bool     `short:"n" help:"Dry run. Print commands without executing."`
	TrustLocalShell bool     `name:"trust" help:"Allow run.args/run.mounts from project-local .dkglaze.yml to be shell-expanded. Can execute arbitrary commands — only use for a trusted .dkglaze.yml."` //nolint:revive,lll // keep descriptive help text
}

func (*RunCmd) Help() string {
	return "Use '--' before args, so args prefixed with '-' get passed to docker run."
}

func (cmd *RunCmd) Run(ctx *Context) error {
	return errors.Wrap(docker.Run(ctx.Ctx, &docker.RunOptions{
		Image:           cmd.Image,
		RunArgs:         cmd.RunArgs,
		Profile:         cmd.Profile,
		Features:        cmd.Features,
		DryRun:          cmd.DryRun,
		TrustLocalShell: cmd.TrustLocalShell,
	}))
}

// CleanCmd is the clean subcommand.
type CleanCmd struct {
	DryRun bool `short:"n" help:"Dry run. Don't actually remove anything."`
}

func (cmd *CleanCmd) Run(ctx *Context) error {
	return errors.Wrap(docker.Clean(ctx.Ctx, docker.CleanOptions{DryRun: cmd.DryRun}))
}

// ListCmd is the list subcommand.
type ListCmd struct{}

func (*ListCmd) Run(ctx *Context) error {
	return errors.Wrap(docker.List(ctx.Ctx))
}

// CompletionCmd prints a shell completion script to stdout.
type CompletionCmd struct {
	Shell string `arg:"" enum:"bash,zsh" help:"Shell to generate completion for"`
}

func (cmd *CompletionCmd) Run(_ *Context, k *kong.Kong) error {
	script, err := generateCompletion(cmd.Shell, k.Model)
	if err != nil {
		return err
	}

	fmt.Print(script)

	return nil
}

// listImagesCmd is a hidden helper used by shell completion scripts.
type listImagesCmd struct{}

func (*listImagesCmd) Run(ctx *Context) error {
	images, err := docker.ListNonDkglazeImages(ctx.Ctx)
	if err != nil {
		return errors.Wrap(err)
	}

	for _, img := range images {
		fmt.Println(img)
	}

	return nil
}

// listProfilesCmd is a hidden helper used by shell completion scripts.
type listProfilesCmd struct{}

func (*listProfilesCmd) Run(_ *Context) error {
	return listConfigNames(func(c *config.Config) map[string]interface{} {
		out := make(map[string]interface{})
		for k := range c.Profiles {
			out[k] = nil
		}

		return out
	})
}

// listFeaturesCmd is a hidden helper used by shell completion scripts.
type listFeaturesCmd struct{}

func (*listFeaturesCmd) Run(_ *Context) error {
	return listConfigNames(func(c *config.Config) map[string]interface{} {
		out := make(map[string]interface{})
		for k := range c.Features {
			out[k] = nil
		}

		return out
	})
}

func listConfigNames(selector func(*config.Config) map[string]interface{}) error {
	conf, err := config.LoadConfig()
	if err != nil {
		return errors.Wrap(err)
	}

	local, err := config.LoadLocalConfig()
	if err != nil {
		return errors.Wrap(err)
	}

	if _, err := conf.MergeLocal(local); err != nil {
		return errors.Wrap(err)
	}

	names := make([]string, 0, len(selector(conf)))
	for name := range selector(conf) {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		fmt.Println(name)
	}

	return nil
}

// version is the dkglaze version string. It is set at build time via
// -ldflags "-X main.version=<version>"; when left as "dev" resolveVersion
// falls back to the module build info.
var version = "dev"

// resolveVersion returns the injected version, or the module version recorded
// in the Go build info (e.g. for `go install module@vX.Y.Z`) when not injected.
func resolveVersion() string {
	if version != "dev" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return version
}

// Cli is the root CLI struct.
type Cli struct {
	Config       ConfigCmd        `cmd:"" help:"Manage configuration"`
	Build        BuildCmd         `cmd:"" help:"Build user layer over docker image"`
	Run          RunCmd           `cmd:"" help:"Run user layer docker image"`
	Clean        CleanCmd         `cmd:"" help:"Remove all images made by this program"`
	List         ListCmd          `cmd:"" help:"List all images made by this program"`
	Completion   CompletionCmd    `cmd:"" help:"Print shell completion script"`
	ListImages   listImagesCmd    `cmd:"" name:"_list-images" hidden:""`
	ListProfiles listProfilesCmd  `cmd:"" name:"_list-profiles" hidden:""`
	ListFeatures listFeaturesCmd  `cmd:"" name:"_list-features" hidden:""`
	Verbose      bool             `help:"Enable debug mode."`
	Version      kong.VersionFlag `name:"version" short:"v" help:"Print version and exit."`
}

func main() {
	var cli Cli

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	kongCtx := kong.Parse(&cli, kong.Vars{"version": resolveVersion()})
	err := kongCtx.Run(&Context{Ctx: ctx, Verbose: cli.Verbose})
	stop()

	if !cli.Verbose {
		err = errors.OuterError(err)
	}

	if err != nil {
		logger.PrintColorF(logger.Red, "%s: %s", filepath.Base(os.Args[0]), err.Error())
		os.Exit(1)
	}
}
