package docker

import (
	"strconv"

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
