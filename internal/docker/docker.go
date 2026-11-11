// Package docker manages docker operations.
package docker

import (
	"context"
	goerrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/containerd/errdefs"
	"github.com/distribution/reference"
	imageapi "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"github.com/future-cyborg/dkglaze/internal/config"
	"github.com/future-cyborg/dkglaze/internal/errors"
	"github.com/future-cyborg/dkglaze/internal/logger"
)

const (
	dockerCmd       = "docker"
	dockerRunCmd    = "run"
	dockerPullCmd   = "pull"
	dockerBuildCmd  = "build"
	dockerLabelFlag = "--label"
	dockerTagFlag   = "-t"
	dockerFileFlag  = "-f"

	// dkglazeLabel marks images built by this program.
	dkglazeLabel = "com.dkglaze"
	// danglingImageTag is how the daemon reports an untagged image.
	danglingImageTag = "<none>:<none>"

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

// Build builds the user layer on top of the image.
func Build(ctx context.Context, opts *BuildOptions) error {
	return withDeps(func(d *deps) error {
		return buildWith(ctx, d, opts)
	})
}

// buildWith is the executor behind Build.
func buildWith(ctx context.Context, d *deps, opts *BuildOptions) error {
	if err := validateImageRef(opts.Image); err != nil {
		return err
	}

	conf, _, err := loadMergedConfig()
	if err != nil {
		return err
	}

	sel := selectProfile(conf, opts.Profile)

	resolved, err := conf.ResolveProfile(ctx, sel.Active, opts.TrustLocalShell)
	if err != nil {
		return errors.Wrap(err)
	}

	plan, err := planBuild(opts.Image, resolved, opts.BuildArgs)
	if err != nil {
		return err
	}

	// A dry run only prints. It must not pull, tag or remove anything.
	if opts.DryRun {
		printBuildPlan(plan)

		return nil
	}

	if opts.Pull {
		if err := pullImage(ctx, d, opts.Image); err != nil {
			return err
		}
	}

	return executeBuild(ctx, d, plan)
}

// executeBuild runs each step in order and then tags the last step's result as
// the final image. Every step that succeeded is recorded so its intermediate tag
// is removed on both success and failure. The cleanup is deferred, so it runs
// after the final tag has been created.
func executeBuild(ctx context.Context, d *deps, plan *buildPlan) (retErr error) {
	var created []string

	defer func() {
		if cleanupErr := cleanupTags(ctx, d.images, created); cleanupErr != nil {
			retErr = errors.Join(retErr, cleanupErr)
		}
	}()

	for i := range plan.Steps {
		step := &plan.Steps[i]

		if err := runBuildStep(ctx, d, step); err != nil {
			return err
		}

		created = append(created, step.ResultTag)
	}

	// planBuild guarantees at least one step, so created is never empty here.
	return tagImage(ctx, d.images, created[len(created)-1], plan.FinalTag)
}

// printBuildPlan prints the command each build step would run, using placeholders
// for the Dockerfile and context.
func printBuildPlan(plan *buildPlan) {
	for i := range plan.Steps {
		logger.PrintColorF(logger.Cyan, "Running '%s'", strings.Join(plan.Steps[i].displayCommand(), " "))
	}
}

func tagImage(ctx context.Context, imgs imageClient, from, to string) error {
	_, err := imgs.ImageTag(ctx, client.ImageTagOptions{
		Source: from,
		Target: to,
	})

	return errors.Wrap(err)
}

// Run runs the user layer on top of the image. Builds it if it doesn't exist.
func Run(ctx context.Context, opts *RunOptions) error {
	return withDeps(func(d *deps) error {
		return runWith(ctx, d, opts)
	})
}

// runWith is the executor behind Run.
func runWith(ctx context.Context, d *deps, opts *RunOptions) error {
	if err := validateImageRef(opts.Image); err != nil {
		return err
	}

	conf, prov, err := loadMergedConfig()
	if err != nil {
		return err
	}

	resolved, err := resolveRun(ctx, conf, prov, opts)
	if err != nil {
		return err
	}

	plan := planRun(opts.Image, resolved, opts.RunArgs)
	logger.PrintColorF(logger.Cyan, "Running '%s'", strings.Join(plan.Command, " "))

	if opts.DryRun {
		return nil
	}

	if err := ensureImage(ctx, d, opts, plan.Image); err != nil {
		return err
	}

	// Exec replaces the process on success, so this must stay the final statement.
	return errors.Wrap(d.execCmd(plan.Command))
}

// resolveRun resolves the selected profile and the -f features into the run
// config, printing the local-provenance warnings along the way.
func resolveRun(
	ctx context.Context, conf *config.Config, prov *config.LocalProvenance, opts *RunOptions,
) (*config.ResolvedConfig, error) {
	sel := selectProfile(conf, opts.Profile)
	printWarnings(localProvenanceWarnings(conf, prov, sel, localConfigLabel()))

	resolved, err := conf.ResolveProfileNoArgs(ctx, sel.Active, opts.TrustLocalShell)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	extra, featureWarnings, err := applyRunFeatures(ctx, conf, prov, opts)
	if err != nil {
		return nil, err
	}

	printWarnings(featureWarnings)

	resolved.Run.Args = append(resolved.Run.Args, extra.Args...)
	resolved.Run.Mounts = append(resolved.Run.Mounts, extra.Mounts...)

	return resolved, nil
}

// ensureImage builds the run image from opts when newImage is not present on the daemon.
func ensureImage(ctx context.Context, d *deps, opts *RunOptions, newImage string) error {
	exists, err := imageExists(ctx, d.images, newImage)
	if err != nil {
		return errors.Wrap(err)
	}

	if exists {
		return nil
	}

	logger.PrintColorErr(logger.Yellow, "Image doesn't exist. Building...")

	// An unset profile falls back to the default inside buildWith, matching the run selection.
	return errors.Wrap(buildWith(ctx, d, &BuildOptions{
		Image: opts.Image, Profile: opts.Profile, TrustLocalShell: opts.TrustLocalShell,
	}))
}

// printWarnings prints each warning in yellow.
func printWarnings(warnings []string) {
	for _, msg := range warnings {
		logger.PrintColor(logger.Yellow, msg)
	}
}

// loadMergedConfig loads the global config and merges the project-local config
// into it, returning the merged config and which names came from the local file.
func loadMergedConfig() (*config.Config, *config.LocalProvenance, error) {
	conf, err := config.LoadConfig()
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}

	local, err := config.LoadLocalConfig()
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}

	prov, err := conf.MergeLocal(local)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}

	return conf, prov, nil
}

// localConfigLabel returns the local config path for use in messages, or a
// fallback label when the path cannot be determined.
func localConfigLabel() string {
	path, _, err := config.GetLocalConfigPath()
	if err != nil {
		return localConfigFallbackLabel
	}

	return path
}

// applyRunFeatures resolves the -f features into the run args and mounts they
// contribute. Build content on a feature is ignored. The returned warnings are
// for the caller to print.
func applyRunFeatures(
	ctx context.Context, conf *config.Config, prov *config.LocalProvenance, opts *RunOptions,
) (config.Run, []string, error) {
	var (
		extra    config.Run
		warnings []string
		label    = localConfigLabel()
	)

	for _, featureName := range opts.Features {
		args, mounts, hasBuild, err := conf.ResolveFeatureRunOnly(ctx, featureName, opts.TrustLocalShell)
		if err != nil {
			return config.Run{}, nil, errors.Wrap(err)
		}

		if hasBuild {
			msg := fmt.Sprintf("feature %q has build config, which is ignored by -f on run", featureName)
			warnings = append(warnings, msg)
		}

		if prov.Features[featureName] {
			msg := fmt.Sprintf("feature %q from local config %s", featureName, label)
			warnings = append(warnings, msg)
		}

		extra.Args = append(extra.Args, args...)
		extra.Mounts = append(extra.Mounts, mounts...)
	}

	return extra, warnings, nil
}

func pullImage(ctx context.Context, d *deps, image string) error {
	command := []string{dockerCmd, dockerPullCmd, image}
	logger.PrintColorF(logger.Cyan, "Pulling '%s'", image)

	return errors.Wrap(d.runCmd(ctx, command))
}

// Clean removes all images made by this program.
func Clean(ctx context.Context, opts CleanOptions) error {
	return withDeps(func(d *deps) error {
		return cleanWith(ctx, d, opts)
	})
}

// cleanWith is the executor behind Clean.
func cleanWith(ctx context.Context, d *deps, opts CleanOptions) error {
	imageItems, err := listDkglazeImages(ctx, d.images)
	if err != nil {
		return err
	}

	if opts.DryRun {
		logger.PrintColor(logger.Cyan, "Would remove:")
	} else {
		logger.PrintColor(logger.Cyan, "Removing:")
	}

	var allErr error

	for i := range imageItems {
		img := &imageItems[i]

		if !opts.DryRun {
			if _, err := d.images.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{}); err != nil {
				allErr = errors.Join(allErr, errors.Wrap(err))

				continue
			}
		}

		logger.PrintColor(logger.Cyan, imageLabel(img))
	}

	if allErr != nil {
		return errors.Wrap(allErr)
	}

	return nil
}

func listDkglazeImages(ctx context.Context, imgs imageClient) ([]imageapi.Summary, error) {
	f := client.Filters{"label": {dkglazeLabel: true}}
	imageList, err := imgs.ImageList(ctx, client.ImageListOptions{Filters: f})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return imageList.Items, nil
}

// ListNonDkglazeImages returns tags of all local Docker images not managed by dkglaze.
func ListNonDkglazeImages(ctx context.Context) ([]string, error) {
	var tags []string

	err := withDeps(func(d *deps) error {
		var err error

		tags, err = listNonDkglazeWith(ctx, d)

		return err
	})

	return tags, err
}

// listNonDkglazeWith is the executor behind ListNonDkglazeImages.
func listNonDkglazeWith(ctx context.Context, d *deps) ([]string, error) {
	imageList, err := d.images.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return nonDkglazeTags(imageList.Items), nil
}

// List prints all images managed by dkglaze.
func List(ctx context.Context) error {
	return withDeps(func(d *deps) error {
		return listWith(ctx, d)
	})
}

// listWith is the executor behind List.
func listWith(ctx context.Context, d *deps) error {
	images, err := listDkglazeImages(ctx, d.images)
	if err != nil {
		return err
	}

	for _, line := range listLines(images) {
		logger.PrintColor(logger.Cyan, line)
	}

	return nil
}

// runBuildStep renders the step's Dockerfile to a temp file and runs docker build with it.
func runBuildStep(ctx context.Context, d *deps, step *buildStep) error {
	tmpFile, contextDir, err := writeTemplateFile(step.Template, step.FromTag)
	if err != nil {
		return errors.Wrap(err)
	}

	defer func() { _ = os.Remove(tmpFile) }()

	command := step.command(tmpFile, contextDir)
	logger.PrintColorF(logger.Cyan, "Running '%s'", strings.Join(command, " "))

	return errors.Wrap(d.runCmd(ctx, command))
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

func cleanupTags(ctx context.Context, imgs imageClient, tags []string) error {
	var allErr error

	for _, tag := range tags {
		logger.PrintColorF(logger.Yellow, "Removing image %s", tag)

		_, err := imgs.ImageRemove(ctx, tag, client.ImageRemoveOptions{})
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

func imageExists(ctx context.Context, imgs imageClient, image string) (bool, error) {
	_, err := imgs.ImageInspect(ctx, image)
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
