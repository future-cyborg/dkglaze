package docker

import (
	"fmt"
	"strconv"

	imageapi "github.com/moby/moby/api/types/image"

	"github.com/future-cyborg/dkglaze/internal/config"
	"github.com/future-cyborg/dkglaze/internal/errors"
)

// buildStep is one `docker build` invocation in a build pipeline.
type buildStep struct {
	// Template is the name of the template rendered into the step's Dockerfile.
	Template string
	// FromTag is the image the rendered Dockerfile starts FROM.
	FromTag string
	// ResultTag is the tag the step produces.
	ResultTag string
	// Args are extra docker build arguments placed before the build context.
	Args []string
}

// buildPlan is the pure description of a build pipeline. Nothing in it touches
// the filesystem or the Docker daemon.
type buildPlan struct {
	// Image is the base image the pipeline starts from.
	Image string
	// Steps run in order; each one builds on the previous step's result.
	Steps []buildStep
	// FinalTag is the name the last step's result is tagged as.
	FinalTag string
}

// planBuild turns the resolved config into a build pipeline with one step per
// template. userArgs are appended to every step's docker build arguments.
func planBuild(image string, resolved *config.ResolvedConfig, userArgs []string) (*buildPlan, error) {
	if len(resolved.Build.Templates) == 0 {
		return nil, errors.Wrap(errNoTemplates)
	}

	args := append(buildArgsFromResolved(resolved), userArgs...)
	plan := &buildPlan{
		Image:    image,
		FinalTag: newImageName(image, resolved.Suffix),
	}

	fromTag := image

	for i, templateName := range resolved.Build.Templates {
		resultTag := newImageName(image, resolved.Suffix+"."+strconv.Itoa(i))

		plan.Steps = append(plan.Steps, buildStep{
			Template:  templateName,
			FromTag:   fromTag,
			ResultTag: resultTag,
			Args:      args,
		})

		fromTag = resultTag
	}

	return plan, nil
}

// command returns the docker build argv for this step. Execution passes the
// real Dockerfile and context paths; display passes placeholders.
func (s *buildStep) command(dockerfile, contextDir string) []string {
	argv := []string{
		dockerCmd, dockerBuildCmd,
		dockerLabelFlag, dkglazeLabel + "=true",
		dockerTagFlag, s.ResultTag,
		dockerFileFlag, dockerfile,
	}
	argv = append(argv, s.Args...)

	return append(argv, contextDir)
}

// displayCommand returns the step's command with placeholders for the
// Dockerfile and context, for printing a dry run.
func (s *buildStep) displayCommand() []string {
	return s.command("<"+s.Template+".dockerfile>", "<context>")
}

// runPlan is the pure description of a docker run invocation.
type runPlan struct {
	// Image is the image the container runs, with the profile suffix applied.
	Image string
	// Command is the full docker run argv.
	Command []string
}

// planRun builds the docker run argv. The resolved config supplies the run
// args and mounts, and runArgs are passed straight through to docker run.
func planRun(image string, resolved *config.ResolvedConfig, runArgs []string) runPlan {
	newImage := newImageName(image, resolved.Suffix)

	return runPlan{
		Image:   newImage,
		Command: runCommand(newImage, resolved.Run.Args, mountFlagsFromResolved(resolved), runArgs),
	}
}

// profileSelection records which profile was requested and which one ends up active
// once the configured default is applied.
type profileSelection struct {
	// Requested is the value of -p, or empty when -p was not given.
	Requested string
	// Active is the profile that is actually used.
	Active string
}

// explicit reports whether the profile was chosen with -p rather than the default.
func (s profileSelection) explicit() bool {
	return s.Requested != ""
}

// selectProfile chooses the active profile: the requested one if given,
// otherwise the configured default.
func selectProfile(conf *config.Config, requested string) profileSelection {
	active := conf.Defaults.Profile
	if requested != "" {
		active = requested
	}

	return profileSelection{Requested: requested, Active: active}
}

// localProvenanceWarnings describes every part of the selected profile
// selection that came from the project-local config file, so a run never
// silently depends on local-sourced configuration. localPath is the path shown
// to the user.
func localProvenanceWarnings(
	conf *config.Config, prov *config.LocalProvenance, sel profileSelection, localPath string,
) []string {
	var warnings []string

	if !sel.explicit() && prov.DefaultFromLocal {
		warnings = append(warnings,
			fmt.Sprintf("profile %q selected by defaults.profile in local config %s", sel.Active, localPath))
	}

	if chain, err := conf.ProfileChain(sel.Active); err == nil {
		for _, name := range chain {
			if prov.Profiles[name] {
				warnings = append(warnings, fmt.Sprintf("using profile %q from local config %s", name, localPath))
			}
		}
	}

	if featureNames, err := conf.ResolveFeatureNames(sel.Active); err == nil {
		for _, featureName := range featureNames {
			if prov.Features[featureName] {
				warnings = append(warnings, fmt.Sprintf("feature %q from local config %s", featureName, localPath))
			}
		}
	}

	return warnings
}

// imageLabel names an image for output: its first tag, or its ID when untagged.
func imageLabel(img *imageapi.Summary) string {
	if len(img.RepoTags) > 0 {
		return img.RepoTags[0]
	}

	return img.ID
}

// listLines returns one output line per tag of each image, or the image ID
// for images without tags.
func listLines(items []imageapi.Summary) []string {
	var lines []string

	for i := range items {
		if len(items[i].RepoTags) > 0 {
			lines = append(lines, items[i].RepoTags...)
		} else {
			lines = append(lines, items[i].ID)
		}
	}

	return lines
}

// nonDkglazeTags returns every real tag of the images that dkglaze does not
// manage. Untagged (dangling) references are skipped.
func nonDkglazeTags(items []imageapi.Summary) []string {
	var tags []string

	for i := range items {
		img := &items[i]

		if img.Labels[dkglazeLabel] != "" {
			continue
		}

		for _, tag := range img.RepoTags {
			if tag == danglingImageTag {
				continue
			}

			tags = append(tags, tag)
		}
	}

	return tags
}
