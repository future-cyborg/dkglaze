package docker

import "github.com/future-cyborg/udocker/internal/config"

func ExportedNewImageName(image, imageTemplate string) string {
	return newImageName(image, imageTemplate)
}

func ExportedRunCommand(image string, runArgs ...[]string) []string {
	return runCommand(image, runArgs...)
}

func ExportedBuildArgsFromResolved(resolved *config.ResolvedConfig) []string {
	return buildArgsFromResolved(resolved)
}

func ExportedMountFlagsFromResolved(resolved *config.ResolvedConfig) []string {
	return mountFlagsFromResolved(resolved)
}

func ExportedWriteTemplateFile(templateName, fromTag string) (tempFile, contextDir string, err error) {
	return writeTemplateFile(templateName, fromTag)
}

func ExportedValidateImageRef(image string) error {
	return validateImageRef(image)
}

func ExportedWarnLocalProvenance(
	conf *config.Config, prov *config.LocalProvenance, profileName string, explicitProfile bool,
) {
	warnLocalProvenance(conf, prov, profileName, explicitProfile)
}
