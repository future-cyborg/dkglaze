package docker

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"github.com/moby/moby/client"

	"github.com/future-cyborg/dkglaze/internal/errors"
)

// imageClient is the subset of the Docker API client used by this package.
// It is satisfied by *client.Client and by test fakes.
type imageClient interface {
	ImageTag(ctx context.Context, options client.ImageTagOptions) (client.ImageTagResult, error)
	ImageRemove(
		ctx context.Context, imageID string, options client.ImageRemoveOptions,
	) (client.ImageRemoveResult, error)
	ImageList(ctx context.Context, options client.ImageListOptions) (client.ImageListResult, error)
	ImageInspect(
		ctx context.Context, imageID string, inspectOpts ...client.ImageInspectOption,
	) (client.ImageInspectResult, error)
}

var _ imageClient = (*client.Client)(nil)

// deps bundles the side-effecting dependencies used by the package's
// operations so that they can be driven by fakes in tests.
type deps struct {
	images  imageClient
	runCmd  func(ctx context.Context, argv []string) error
	execCmd func(argv []string) error
}

// withDeps opens the real Docker client, hands fn a deps wired to the real
// process helpers, and closes the client when fn returns.
func withDeps(fn func(*deps) error) error {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return errors.Wrap(err)
	}

	defer func() { _ = cli.Close() }()

	return fn(&deps{images: cli, runCmd: execCommand, execCmd: execReplace})
}

// execCommand runs command as a child process, streaming its stdio to ours.
func execCommand(ctx context.Context, command []string) error {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...) //nolint:gosec // trusted config
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return errors.Wrap(cmd.Run())
}

// execReplace replaces the current process with argv. It only returns on failure.
func execReplace(argv []string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return errors.Wrap(err)
	}

	return errors.Wrap(syscall.Exec(path, argv, os.Environ())) //nolint:gosec // trusted config
}
