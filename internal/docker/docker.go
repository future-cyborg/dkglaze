// Package docker manages docker operations.
package docker

import (
	"context"
	goerrors "errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/template"

	"github.com/containerd/errdefs"
	"github.com/distribution/reference"
	imageapi "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"github.com/future-cyborg/ldocker/internal/config"
	"github.com/future-cyborg/ldocker/internal/errors"
	"github.com/future-cyborg/ldocker/internal/logger"
)

const (
	dockerCmd    = "docker"
	dockerRunCmd = "run"

	// localConfigFallbackLabel is used in warning messages when the local
	// config path itself cannot be determined.
	localConfigFallbackLabel = "local config"
)

var errNoTemplates = goerrors.New("no templates specified")

var errInvalidImageRef = goerrors.New("invalid image reference")

func validateImageRef(image string) error {
	if _, err := reference.ParseNormalizedNamed(image); err != nil {
		return errors.Wrap(fmt.Errorf("%w: %w", errInvalidImageRef, err))
	}

	return nil
}

type templateBuild struct {
	resolved  *config.ResolvedConfig
	name      string
	fromTag   string
	resultTag string
	buildArgs []string
	dryRun    bool
}

func newDockerClient() (*client.Client, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return cli, nil
}

func withDockerClient(fn func(*client.Client) error) error {
	cli, err := newDockerClient()
	if err != nil {
		return err
	}

	defer func() { _ = cli.Close() }()

	return fn(cli)
}

// Build builds the user layer on top of the image.
//
//nolint:revive // flag-parameter is the clearest API here
func Build(ctx context.Context, image string, buildArgs []string, profile string, pull, dryRun bool) (retErr error) {
	if err := validateImageRef(image); err != nil {
		return err
	}

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

	activeProfile := conf.Defaults.Profile
	if profile != "" {
		activeProfile = profile
	}

	resolved, err := conf.ResolveProfile(ctx, activeProfile)
	if err != nil {
		return errors.Wrap(err)
	}

	if len(resolved.Build.Templates) == 0 {
		return errors.Wrap(errNoTemplates)
	}

	cli, err := newDockerClient()
	if err != nil {
		return errors.Wrap(err)
	}

	defer func() { _ = cli.Close() }()

	if pull {
		if err := pullImage(ctx, image); err != nil {
			return err
		}
	}

	finalTag, intermediateTags, err := buildTemplates(ctx, resolved, image, buildArgs, dryRun)
	// Intermediate tags are temporary stepping-stone images created during the
	// build pipeline. They are safe to remove on both success and failure: on
	// success the final image has already been retagged, and on failure the
	// partial intermediates are no longer needed.
	defer func() {
		if cleanupErr := cleanupTags(ctx, cli, intermediateTags); cleanupErr != nil {
			retErr = errors.Join(retErr, cleanupErr)
		}
	}()

	if err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	return tagImage(ctx, cli, finalTag, newImageName(image, resolved.Suffix))
}

//nolint:revive // argument-limit: dryRun is a necessary addition to this internal function
func buildTemplates(
	ctx context.Context, resolved *config.ResolvedConfig, image string, buildArgs []string, dryRun bool,
) (finalTag string, intermediateTags []string, err error) {
	fromTag := image

	for i, templateName := range resolved.Build.Templates {
		resultTag := newImageName(image, resolved.Suffix+"."+strconv.Itoa(i))

		t := &templateBuild{
			resolved: resolved, name: templateName,
			fromTag: fromTag, resultTag: resultTag,
			buildArgs: buildArgs, dryRun: dryRun,
		}

		if err := buildTemplate(ctx, t); err != nil {
			return "", intermediateTags, errors.Wrap(err)
		}

		intermediateTags = append(intermediateTags, resultTag)
		fromTag = resultTag
	}

	return fromTag, intermediateTags, nil
}

func tagImage(ctx context.Context, cli *client.Client, from, to string) error {
	_, err := cli.ImageTag(ctx, client.ImageTagOptions{
		Source: from,
		Target: to,
	})

	return errors.Wrap(err)
}

// Run runs the user layer on top of the image. Builds it if it doesn't exist.
//
//nolint:revive,gocognit,funlen // flag-parameter is clearest; local-config merging adds branching/length
func Run(ctx context.Context, image string, runArgs []string, profile string, features []string, dryRun bool) error {
	if err := validateImageRef(image); err != nil {
		return err
	}

	conf, err := config.LoadConfig()
	if err != nil {
		return errors.Wrap(err)
	}

	local, err := config.LoadLocalConfig()
	if err != nil {
		return errors.Wrap(err)
	}
	prov, err := conf.MergeLocal(local)
	if err != nil {
		return errors.Wrap(err)
	}

	activeProfile := conf.Defaults.Profile
	if profile != "" {
		activeProfile = profile
	}

	warnLocalProvenance(conf, prov, activeProfile, profile != "")

	resolved, err := conf.ResolveProfileNoArgs(ctx, activeProfile)
	if err != nil {
		return errors.Wrap(err)
	}

	// Apply any feature-level Run configuration from -f flags
	for _, featureName := range features {
		args, mounts, hasBuild, err := conf.ResolveFeatureRunOnly(ctx, featureName)
		if err != nil {
			return errors.Wrap(err)
		}

		if hasBuild {
			logger.PrintColorF(logger.Yellow, "feature %q has build config, which is ignored by -f on run", featureName)
		}

		if prov.Features[featureName] {
			path, _, err := config.GetLocalConfigPath()
			if err != nil {
				path = localConfigFallbackLabel
			}
			logger.PrintColorF(logger.Yellow, "feature %q from local config %s", featureName, path)
		}

		resolved.Run.Args = append(resolved.Run.Args, args...)
		resolved.Run.Mounts = append(resolved.Run.Mounts, mounts...)
	}

	return withDockerClient(func(cli *client.Client) error {
		newImage := newImageName(image, resolved.Suffix)

		command := runCommand(newImage, resolved.Run.Args, mountFlagsFromResolved(resolved), runArgs)
		logger.PrintColorF(logger.Cyan, "Running '%s'", strings.Join(command, " "))

		if dryRun {
			return nil
		}

		exists, err := imageExists(ctx, cli, newImage)
		if err != nil {
			return errors.Wrap(err)
		}

		if !exists {
			logger.PrintColorErr(logger.Yellow, "Image doesn't exist. Building...")

			if err = Build(ctx, image, nil, profile, false, false); err != nil {
				return errors.Wrap(err)
			}
		}

		path, err := exec.LookPath(command[0])
		if err != nil {
			return errors.Wrap(err)
		}

		return errors.Wrap(syscall.Exec(path, command, os.Environ())) //nolint:gosec // trusted config
	})
}

func execCommand(ctx context.Context, command []string) error {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...) //nolint:gosec // trusted config
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return errors.Wrap(cmd.Run())
}

func pullImage(ctx context.Context, image string) error {
	command := []string{dockerCmd, "pull", image}
	logger.PrintColorF(logger.Cyan, "Pulling '%s'", image)

	return execCommand(ctx, command)
}

// Clean removes all images made by this program.
//
//nolint:revive // flag-parameter is the clearest API here
func Clean(ctx context.Context, dryRun bool) error {
	return withDockerClient(func(cli *client.Client) error {
		imageItems, err := listLdockerImages(ctx, cli)
		if err != nil {
			return err
		}

		if dryRun {
			logger.PrintColor(logger.Cyan, "Would remove:")
		} else {
			logger.PrintColor(logger.Cyan, "Removing:")
		}

		var allErr error

		for item := range imageItems {
			err = cleanImage(ctx, cli, &imageItems[item], dryRun)
			if err != nil {
				allErr = errors.Join(allErr, err)
			}
		}

		if allErr != nil {
			return errors.Wrap(allErr)
		}

		return nil
	})
}

func listLdockerImages(ctx context.Context, cli *client.Client) ([]imageapi.Summary, error) {
	f := client.Filters{"label": {"com.ldocker": true}}
	imageList, err := cli.ImageList(ctx, client.ImageListOptions{Filters: f})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return imageList.Items, nil
}

// ListNonLdockerImages returns tags of all local Docker images not managed by ldocker.
func ListNonLdockerImages(ctx context.Context) ([]string, error) {
	var tags []string

	err := withDockerClient(func(cli *client.Client) error {
		imageList, err := cli.ImageList(ctx, client.ImageListOptions{})
		if err != nil {
			return errors.Wrap(err)
		}

		for i := range imageList.Items {
			img := &imageList.Items[i]

			if img.Labels["com.ldocker"] != "" {
				continue
			}

			for _, tag := range img.RepoTags {
				if tag == "<none>:<none>" {
					continue
				}

				tags = append(tags, tag)
			}
		}

		return nil
	})

	return tags, err
}

// List prints all images managed by ldocker.
func List(ctx context.Context) error {
	return withDockerClient(func(cli *client.Client) error {
		images, err := listLdockerImages(ctx, cli)
		if err != nil {
			return err
		}

		for i := range images {
			if len(images[i].RepoTags) > 0 {
				for _, tag := range images[i].RepoTags {
					logger.PrintColor(logger.Cyan, tag)
				}
			} else {
				logger.PrintColor(logger.Cyan, images[i].ID)
			}
		}

		return nil
	})
}

//nolint:revive // flag-parameter is the clearest API here
func cleanImage(ctx context.Context, cli *client.Client, image *imageapi.Summary, dryRun bool) error {
	if !dryRun {
		_, err := cli.ImageRemove(ctx, image.ID, client.ImageRemoveOptions{})
		if err != nil {
			return errors.Wrap(err)
		}
	}

	if len(image.RepoTags) > 0 {
		logger.PrintColor(logger.Cyan, image.RepoTags[0])
	} else {
		logger.PrintColor(logger.Cyan, image.ID)
	}

	return nil
}

func buildTemplate(ctx context.Context, t *templateBuild) error {
	placeholderOrReal := "<" + t.name + ".dockerfile>"

	command := []string{
		dockerCmd, "build",
		"--label", "com.ldocker=true",
		"-t", t.resultTag,
		"-f", placeholderOrReal,
	}

	command = append(command, buildArgsFromResolved(t.resolved)...)
	command = append(command, t.buildArgs...)

	if t.dryRun {
		command = append(command, "<context>")
		logger.PrintColorF(logger.Cyan, "Running '%s'", strings.Join(command, " "))

		return nil
	}

	tmpFile, contextDir, err := writeTemplateFile(t.name, t.fromTag)
	if err != nil {
		return errors.Wrap(err)
	}

	defer func() { _ = os.Remove(tmpFile) }()

	command[7] = tmpFile
	command = append(command, contextDir)

	logger.PrintColorF(logger.Cyan, "Running '%s'", strings.Join(command, " "))

	cmd := exec.CommandContext(ctx, command[0], command[1:]...) //nolint:gosec // trusted config

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return errors.Wrap(cmd.Run())
}

func writeTemplateFile(templateName, fromTag string) (tempFile, contextDir string, err error) {
	configFiles, err := config.GetConfigPaths()
	if err != nil {
		return "", "", errors.Wrap(err)
	}

	tmplFile := filepath.Join(configFiles.TemplatesDir, templateName+".dockerfile")
	if _, err = os.Stat(tmplFile); err != nil {
		return "", "", errors.Wrap(err)
	}

	tmpl, err := template.ParseFiles(tmplFile)
	if err != nil {
		return "", "", errors.Wrap(err)
	}

	outFile, err := executeTemplate(tmpl, templateName, fromTag)
	if err != nil {
		return "", "", err
	}

	contextDir = filepath.Join(configFiles.TemplatesDir, templateName)
	if _, err := os.Stat(contextDir); err != nil {
		if !os.IsNotExist(err) {
			return "", "", errors.Wrap(err)
		}

		contextDir = configFiles.TemplatesDir
	}

	return outFile, contextDir, nil
}

func executeTemplate(tmpl *template.Template, templateName, fromTag string) (string, error) {
	tmpName := fmt.Sprintf("%s.*.dockerfile", templateName)

	tmpFile, err := os.CreateTemp("", tmpName)
	if err != nil {
		return "", errors.Wrap(err)
	}

	err = tmpl.Execute(tmpFile, map[string]interface{}{
		"image": fromTag,
	})
	if err != nil {
		_ = tmpFile.Close()

		return "", errors.Wrap(err)
	}

	if err = tmpFile.Close(); err != nil {
		return "", errors.Wrap(err)
	}

	return tmpFile.Name(), nil
}

func cleanupTags(ctx context.Context, cli *client.Client, tags []string) error {
	var allErr error

	for _, tag := range tags {
		logger.PrintColorF(logger.Yellow, "Removing image %s", tag)

		_, err := cli.ImageRemove(ctx, tag, client.ImageRemoveOptions{})
		if err != nil {
			logger.PrintColorFErr(logger.Red, "Failed to remove image %s: %v", tag, err)
			allErr = errors.Join(allErr, errors.Wrap(err))
		}
	}

	return allErr
}

func newImageName(image, imageTemplate string) string {
	// Strip digest if present (e.g. ubuntu:22.04@sha256:abc... → ubuntu:22.04).
	// Digests cannot be used as tag targets, but the tag portion (if any) is preserved.
	if atIdx := strings.Index(image, "@"); atIdx != -1 {
		image = image[:atIdx]
	}

	// Find the last ':' that is not part of a registry host (i.e., after any '/')
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")

	if lastColon <= lastSlash {
		return fmt.Sprintf("%s-%s:latest", image, imageTemplate)
	}

	return fmt.Sprintf("%s-%s:%s", image[:lastColon], imageTemplate, image[lastColon+1:])
}

// warnLocalProvenance prints a loud, non-blocking warning whenever the
// resolved profile, any profile it extends, any of their features, or the
// default-profile selection itself originated from the local project-tree
// config file, so a run never silently depends on local-sourced
// configuration. explicitProfile is true when the caller passed -p, in which
// case the local defaults.profile (if any) played no part in the selection.
//
//nolint:revive // flag-parameter is the clearest API here
func warnLocalProvenance(conf *config.Config, prov *config.LocalProvenance, profileName string, explicitProfile bool) {
	path, _, err := config.GetLocalConfigPath()
	if err != nil {
		path = localConfigFallbackLabel
	}

	if !explicitProfile && prov.DefaultFromLocal {
		logger.PrintColorF(logger.Yellow, "profile %q selected by defaults.profile in local config %s", profileName, path)
	}

	if chain, err := conf.ProfileChain(profileName); err == nil {
		for _, name := range chain {
			if prov.Profiles[name] {
				logger.PrintColorF(logger.Yellow, "using profile %q from local config %s", name, path)
			}
		}
	}

	if featureNames, err := conf.ResolveFeatureNames(profileName); err == nil {
		for _, featureName := range featureNames {
			if prov.Features[featureName] {
				logger.PrintColorF(logger.Yellow, "feature %q from local config %s", featureName, path)
			}
		}
	}
}

func buildArgsFromResolved(resolved *config.ResolvedConfig) []string {
	var buildArgs []string
	for _, arg := range resolved.Build.Args {
		buildArgs = append(buildArgs, "--build-arg", fmt.Sprintf("%s=%s", arg.Key, arg.Value))
	}

	return buildArgs
}

func mountFlagsFromResolved(resolved *config.ResolvedConfig) []string {
	var flags []string
	for _, mount := range resolved.Run.Mounts {
		flags = append(flags, "--volume", mount)
	}

	return flags
}

func imageExists(ctx context.Context, cli *client.Client, image string) (bool, error) {
	_, err := cli.ImageInspect(ctx, image)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}

		return false, errors.Wrap(err)
	}

	return true, nil
}

func runCommand(image string, runArgs ...[]string) []string {
	command := []string{
		dockerCmd, dockerRunCmd,
	}

	for _, arg := range runArgs {
		command = append(command, arg...)
	}

	command = append(command, image)

	return command
}
