package docker

import "github.com/future-cyborg/udocker/internal/config"

func ExportedNewImageName(image, imageTemplate string) string {
	return newImageName(image, imageTemplate)
}

func ExportedRunCommand(image string, runArgs ...[]string) []string {
	return runCommand(image, runArgs...)
}

func ExportedBuildArgsFromConfig(conf *config.Config) []string {
	return buildArgsFromConfig(conf)
}

func ExportedWriteTemplateFile(templateName, fromTag string) (tempFile, contextDir string, err error) {
	return writeTemplateFile(templateName, fromTag)
}

func ExportedValidateImageRef(image string) error {
	return validateImageRef(image)
}
