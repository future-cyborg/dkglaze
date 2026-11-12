package docker

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// inspectContext returns the JSON output of `docker context inspect` for the
// active context. It is a variable so tests can substitute a fake instead of
// shelling out to the real docker CLI.
//
//nolint:gochecknoglobals // swapped by tests to fake the docker CLI
var inspectContext = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "context", "inspect").Output()
}

// defaultContextName is the name the docker CLI gives its built-in context.
const defaultContextName = "default"

var (
	errContextInspect          = goerrors.New("docker context inspect failed")
	errUnexpectedContextOutput = goerrors.New("unexpected `docker context inspect` output")
	errSSHContext              = goerrors.New(
		"ssh:// docker contexts are not supported by dkglaze clean/list/image tagging " +
			"(build and run use the docker CLI and still work)",
	)
	errSkipTLSVerifyContext = goerrors.New(
		"docker contexts with skip-tls-verify are not supported by dkglaze clean/list/image tagging",
	)
)

// ctxInfo is the subset of `docker context inspect` output that dkglaze uses.
// Unknown fields are ignored.
type ctxInfo struct {
	Name      string `json:"Name"`
	Endpoints struct {
		Docker struct {
			Host          string `json:"Host"`
			SkipTLSVerify bool   `json:"SkipTLSVerify"`
		} `json:"docker"`
	} `json:"Endpoints"`
	TLSMaterial map[string][]string `json:"TLSMaterial"`
	Storage     struct {
		TLSPath string `json:"TLSPath"`
	} `json:"Storage"`
}

// clientOpts resolves the options for the SDK client so that it targets the
// same daemon the docker CLI would. The docker CLI applies its own precedence
// (DOCKER_CONTEXT, DOCKER_HOST, then the config's currentContext), so the
// active context is obtained from it rather than re-derived here.
func clientOpts(ctx context.Context) ([]client.Opt, error) {
	// Explicit DOCKER_HOST without DOCKER_CONTEXT: the docker CLI uses it
	// verbatim, which matches client.FromEnv exactly. Skip the subprocess.
	if os.Getenv("DOCKER_HOST") != "" && os.Getenv("DOCKER_CONTEXT") == "" {
		return []client.Opt{client.FromEnv}, nil
	}

	out, err := inspectContext(ctx)
	if err != nil {
		return nil, errors.Wrap(fmt.Errorf("%w: %w%s", errContextInspect, err, stderrSuffix(err)))
	}

	var infos []ctxInfo
	if jsonErr := json.Unmarshal(out, &infos); jsonErr != nil {
		return nil, errors.Wrap(fmt.Errorf("%w: %w", errUnexpectedContextOutput, jsonErr))
	}

	if len(infos) != 1 {
		return nil, errors.Wrap(fmt.Errorf("%w: expected 1 context, got %d", errUnexpectedContextOutput, len(infos)))
	}

	return optsForContext(&infos[0])
}

// optsForContext maps a resolved docker context to SDK client options.
func optsForContext(info *ctxInfo) ([]client.Opt, error) {
	// The default context has no TLS files on disk; env vars are authoritative.
	if info.Name == defaultContextName {
		return []client.Opt{client.FromEnv}, nil
	}

	host := info.Endpoints.Docker.Host
	if host == "" {
		return nil, errors.Wrap(fmt.Errorf("%w: context %q has no docker host", errUnexpectedContextOutput, info.Name))
	}

	if strings.HasPrefix(host, "ssh://") {
		return nil, errors.Wrap(fmt.Errorf("%w: context %q uses %s", errSSHContext, info.Name, host))
	}

	// skip-tls-verify is only meaningful for TLS-capable (non-unix) hosts. Rather
	// than silently building an insecure client, refuse explicitly.
	if info.Endpoints.Docker.SkipTLSVerify && !strings.HasPrefix(host, "unix://") {
		return nil, errors.Wrap(fmt.Errorf("%w: context %q", errSkipTLSVerifyContext, info.Name))
	}

	opts := []client.Opt{client.WithHost(host), client.WithAPIVersionFromEnv()}

	if len(info.TLSMaterial["docker"]) > 0 && info.Storage.TLSPath != "" {
		dir := filepath.Join(info.Storage.TLSPath, "docker")
		opts = append(opts, client.WithTLSClientConfig(
			filepath.Join(dir, "ca.pem"),
			filepath.Join(dir, "cert.pem"),
			filepath.Join(dir, "key.pem"),
		))
	}

	return opts, nil
}

// stderrSuffix returns the captured stderr of a failed subprocess, if any, so
// that the wrapped error says why `docker context inspect` failed.
func stderrSuffix(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}

	msg := strings.TrimSpace(string(exitErr.Stderr))
	if msg == "" {
		return ""
	}

	return ": " + msg
}

// withDeps opens the real Docker client, hands fn a deps wired to the real
// process helpers, and closes the client when fn returns.
func withDeps(fn func(*deps) error) error {
	opts, err := clientOpts(context.Background())
	if err != nil {
		return err
	}

	cli, err := client.New(opts...)
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
