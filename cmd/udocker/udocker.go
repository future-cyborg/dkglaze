// Package main is the entry point for the udocker CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alecthomas/kong"

	"github.com/future-cyborg/udocker/internal/docker"
	"github.com/future-cyborg/udocker/internal/errors"
	"github.com/future-cyborg/udocker/internal/logger"
)

// Context holds global CLI context.
type Context struct {
	Ctx     context.Context
	Verbose bool
}

// BuildCmd is the build subcommand.
type BuildCmd struct {
	Image     string   `arg:"" name:"image" help:"Image to build FROM"`
	BuildArgs []string `arg:"" optional:"" name:"build-args" help:"Build arguments passed to docker build."`
	Profile   string   `short:"p" name:"profile" help:"Profile to activate (overrides defaults.profile)."`
	Pull      bool     `name:"pull" help:"Pull the latest base image before building."`
}

func (*BuildCmd) Help() string {
	return "Use '--' before build-args, so args prefixed with '-' get passed to docker build."
}

func (cmd *BuildCmd) Run(ctx *Context) error {
	return errors.Wrap(docker.Build(ctx.Ctx, cmd.Image, cmd.BuildArgs, cmd.Profile, cmd.Pull))
}

// RunCmd is the run subcommand.
type RunCmd struct {
	Image   string   `arg:"" name:"image" help:"Image to run user-layer on top of"`
	RunArgs []string `arg:"" optional:"" name:"args" help:"Arguments passed to docker run"`
	Profile string   `short:"p" name:"profile" help:"Profile to activate (overrides defaults.profile)."`
}

func (*RunCmd) Help() string {
	return "Use '--' before args, so args prefixed with '-' get passed to docker run."
}

func (cmd *RunCmd) Run(ctx *Context) error {
	return errors.Wrap(docker.Run(ctx.Ctx, cmd.Image, cmd.RunArgs, cmd.Profile))
}

// CleanCmd is the clean subcommand.
type CleanCmd struct {
	DryRun bool `short:"n" help:"Dry run. Don't actually remove anything."`
}

func (cmd *CleanCmd) Run(ctx *Context) error {
	return errors.Wrap(docker.Clean(ctx.Ctx, cmd.DryRun))
}

// ListCmd is the list subcommand.
type ListCmd struct{}

func (*ListCmd) Run(ctx *Context) error {
	return errors.Wrap(docker.List(ctx.Ctx))
}

// Cli is the root CLI struct.
type Cli struct {
	Config  ConfigCmd `cmd:"" help:"Manage configuration"`
	Build   BuildCmd  `cmd:"" help:"Build user layer over docker image"`
	Run     RunCmd    `cmd:"" help:"Run user layer docker image"`
	Clean   CleanCmd  `cmd:"" help:"Remove all images made by this program"`
	List    ListCmd   `cmd:"" help:"List all images made by this program"`
	Verbose bool      `help:"Enable debug mode."`
}

func main() {
	var cli Cli

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	kongCtx := kong.Parse(&cli)
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
