package docker

// BuildOptions are the CLI-facing inputs to Build.
type BuildOptions struct {
	Image           string
	BuildArgs       []string
	Profile         string
	Pull            bool
	DryRun          bool
	TrustLocalShell bool
}

// RunOptions are the CLI-facing inputs to Run.
type RunOptions struct {
	Image           string
	RunArgs         []string
	Profile         string
	Features        []string
	DryRun          bool
	TrustLocalShell bool
}

// CleanOptions are the CLI-facing inputs to Clean.
type CleanOptions struct {
	DryRun bool
}
