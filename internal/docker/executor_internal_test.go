package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/containerd/errdefs"
	imageapi "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

var errFake = errors.New("fake failure")

// fakeImages implements imageClient, recording each call as a string and
// scripting results. exists drives ImageInspect; NotFound is reported the way
// the real client reports it (errdefs.ErrNotFound).
type fakeImages struct {
	calls     []string
	exists    map[string]bool
	items     []imageapi.Summary
	listErr   error
	tagErr    error
	removeErr map[string]error
}

func (f *fakeImages) ImageTag(_ context.Context, o client.ImageTagOptions) (client.ImageTagResult, error) {
	f.calls = append(f.calls, fmt.Sprintf("tag %s %s", o.Source, o.Target))

	return client.ImageTagResult{}, f.tagErr
}

func (f *fakeImages) ImageRemove(
	_ context.Context, id string, _ client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	f.calls = append(f.calls, "remove "+id)

	return client.ImageRemoveResult{}, f.removeErr[id]
}

func (f *fakeImages) ImageList(_ context.Context, _ client.ImageListOptions) (client.ImageListResult, error) {
	f.calls = append(f.calls, "list")

	return client.ImageListResult{Items: f.items}, f.listErr
}

func (f *fakeImages) ImageInspect(
	_ context.Context, id string, _ ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	f.calls = append(f.calls, "inspect "+id)
	if !f.exists[id] {
		return client.ImageInspectResult{}, fmt.Errorf("no such image %s: %w", id, errdefs.ErrNotFound)
	}

	return client.ImageInspectResult{}, nil
}

// fakeCmds records process invocations. Calls whose zero-based index is a key
// of failAt return the mapped error. For docker build steps it also captures
// the rendered Dockerfile (the temp file is removed after the step) and
// redacts that temp path in argvs so expectations are deterministic.
type fakeCmds struct {
	argvs       [][]string
	dockerfiles []string
	failAt      map[int]error
}

func (c *fakeCmds) record(argv []string) error {
	idx := len(c.argvs)
	call := slices.Clone(argv)

	if len(call) > 7 && call[1] == dockerBuildCmd && call[6] == dockerFileFlag {
		content, _ := os.ReadFile(call[7]) //nolint:gosec // temp Dockerfile written by this package
		c.dockerfiles = append(c.dockerfiles, string(content))
		call[7] = "<dockerfile>"
	}

	c.argvs = append(c.argvs, call)

	return c.failAt[idx]
}

func (c *fakeCmds) run(_ context.Context, argv []string) error { return c.record(argv) }

func (c *fakeCmds) exec(argv []string) error { return c.record(argv) }

// harness bundles the fakes and wires them into a *deps.
type harness struct {
	imgs  fakeImages
	cmds  fakeCmds // docker build/pull invocations (deps.runCmd)
	execs fakeCmds // final replace-process invocation (deps.execCmd)
}

func (h *harness) newDeps() *deps {
	return &deps{images: &h.imgs, runCmd: h.cmds.run, execCmd: h.execs.exec}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// setupConfig points config and local-config lookups at a temp dir, writes the
// given config.yml plus templates a and b, and returns the templates dir.
func setupConfig(t *testing.T, configYAML string) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("DKGLAZE_CONFIG_DIR", dir)

	localPath := filepath.Join(dir, "local.yml")
	writeTestFile(t, localPath, "{}\n")
	t.Setenv("DKGLAZE_LOCAL_CONFIG", localPath)

	writeTestFile(t, filepath.Join(dir, "config.yml"), configYAML)

	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0o750); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}

	for _, name := range []string{"a", "b"} {
		writeTestFile(t, filepath.Join(templates, name+".dockerfile"), "FROM {{.image}}\n")
	}

	return templates
}

func assertArgvs(t *testing.T, got, want [][]string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d calls, want %d:\n got:  %q\n want: %q", len(got), len(want), got, want)
	}

	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("call %d:\n got:  %q\n want: %q", i, got[i], want[i])
		}
	}
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("image calls:\n got:  %q\n want: %q", got, want)
	}
}

const multiConfig = `defaults:
  suffix: test
  profile: default
features:
  base:
    build:
      templates: [a, b]
      args:
        - key: FOO
          value: bar
    run:
      args: ["--rm"]
      mounts: ["/host:/container"]
  extra:
    build:
      templates: []
      args: []
    run:
      args: ["-e", "X=1"]
      mounts: ["/a:/a"]
profiles:
  default:
    features:
      - base
`

const noTemplatesConfig = `defaults:
  suffix: test
  profile: default
features:
  plain:
    build:
      templates: []
      args: []
    run:
      args: []
      mounts: []
profiles:
  default:
    features:
      - plain
`

// Expected image names for multiConfig with the default profile.
const (
	tagStep0 = "ubuntu-test-default.0:22.04"
	tagStep1 = "ubuntu-test-default.1:22.04"
	tagFinal = "ubuntu-test-default:22.04"
	tagRun   = "ubuntu-test-default:22.04"
)

func buildStepArgv(resultTag, contextDir string, extra ...string) []string {
	argv := []string{
		dockerCmd, dockerBuildCmd, dockerLabelFlag, dkglazeLabel + "=true",
		dockerTagFlag, resultTag, dockerFileFlag, "<dockerfile>",
		"--build-arg", "FOO=bar",
	}

	argv = append(argv, extra...)

	return append(argv, contextDir)
}

func TestBuildWith_MultiTemplateSuccess(t *testing.T) {
	templates := setupConfig(t, multiConfig)
	h := &harness{}

	err := buildWith(context.Background(), h.newDeps(), &BuildOptions{
		Image: "ubuntu:22.04", BuildArgs: []string{"--no-cache"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgvs(t, h.cmds.argvs, [][]string{
		buildStepArgv(tagStep0, templates, "--no-cache"),
		buildStepArgv(tagStep1, templates, "--no-cache"),
	})

	// Each step must render FROM the previous step's tag, starting from the base image.
	wantDockerfiles := []string{"FROM ubuntu:22.04\n", "FROM " + tagStep0 + "\n"}
	if !slices.Equal(h.cmds.dockerfiles, wantDockerfiles) {
		t.Errorf("dockerfiles: got %q, want %q", h.cmds.dockerfiles, wantDockerfiles)
	}

	// Tag the final image before removing the intermediates.
	assertCalls(t, h.imgs.calls, []string{
		"tag " + tagStep1 + " " + tagFinal,
		"remove " + tagStep0,
		"remove " + tagStep1,
	})
}

func TestBuildWith_PullWhenRequested(t *testing.T) {
	templates := setupConfig(t, multiConfig)
	h := &harness{}

	err := buildWith(context.Background(), h.newDeps(), &BuildOptions{Image: "ubuntu:22.04", Pull: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgvs(t, h.cmds.argvs, [][]string{
		{dockerCmd, dockerPullCmd, "ubuntu:22.04"},
		buildStepArgv(tagStep0, templates),
		buildStepArgv(tagStep1, templates),
	})
}

// BUG(M6): build --dry-run still runs `docker pull` when --pull is given.
// Fixed in the build-dry-run commit; this pins the current behavior.
//
//nolint:godox // pinned characterization of a known bug; inverted by the fix commit
func TestBuildWith_DryRunPullBug(t *testing.T) {
	setupConfig(t, multiConfig)
	h := &harness{}

	err := buildWith(context.Background(), h.newDeps(), &BuildOptions{
		Image: "ubuntu:22.04", Pull: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgvs(t, h.cmds.argvs, [][]string{{dockerCmd, dockerPullCmd, "ubuntu:22.04"}})
}

// BUG(M6): build --dry-run records intermediate tags that were never created, so
// the deferred cleanup calls ImageRemove on them and, with a NotFound error from
// the daemon, dry-run incorrectly returns an error. Fixed in the build-dry-run commit.
//
//nolint:godox // pinned characterization of a known bug; inverted by the fix commit
func TestBuildWith_DryRunCleanupBug(t *testing.T) {
	setupConfig(t, multiConfig)
	h := &harness{}
	notFound := fmt.Errorf("no such image: %w", errdefs.ErrNotFound)
	h.imgs.removeErr = map[string]error{tagStep0: notFound, tagStep1: notFound}

	err := buildWith(context.Background(), h.newDeps(), &BuildOptions{Image: "ubuntu:22.04", DryRun: true})
	if err == nil {
		t.Fatal("expected pinned dry-run cleanup error, got nil")
	}

	assertCalls(t, h.imgs.calls, []string{"remove " + tagStep0, "remove " + tagStep1})
}

func TestBuildWith_DryRunNeverTagsFinalImage(t *testing.T) {
	setupConfig(t, multiConfig)
	h := &harness{}

	err := buildWith(context.Background(), h.newDeps(), &BuildOptions{Image: "ubuntu:22.04", DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, call := range h.imgs.calls {
		if strings.HasPrefix(call, "tag ") {
			t.Errorf("dry-run must not tag, got call %q", call)
		}
	}
}

func TestBuildWith_MidPipelineFailureStopsAndCleansEarlierSteps(t *testing.T) {
	setupConfig(t, multiConfig)
	h := &harness{}
	h.cmds.failAt = map[int]error{1: errFake}

	err := buildWith(context.Background(), h.newDeps(), &BuildOptions{Image: "ubuntu:22.04"})
	if !errors.Is(err, errFake) {
		t.Fatalf("want errFake, got %v", err)
	}

	if len(h.cmds.argvs) != 2 {
		t.Errorf("later steps must not run: got %d build calls, want 2", len(h.cmds.argvs))
	}

	assertCalls(t, h.imgs.calls, []string{"remove " + tagStep0})
}

func TestBuildWith_TagFailureStillCleansUp(t *testing.T) {
	setupConfig(t, multiConfig)
	h := &harness{}
	h.imgs.tagErr = errFake

	err := buildWith(context.Background(), h.newDeps(), &BuildOptions{Image: "ubuntu:22.04"})
	if !errors.Is(err, errFake) {
		t.Fatalf("want errFake, got %v", err)
	}

	assertCalls(t, h.imgs.calls, []string{
		"tag " + tagStep1 + " " + tagFinal,
		"remove " + tagStep0,
		"remove " + tagStep1,
	})
}

func TestBuildWith_NoTemplatesReturnsError(t *testing.T) {
	setupConfig(t, noTemplatesConfig)
	h := &harness{}

	err := buildWith(context.Background(), h.newDeps(), &BuildOptions{Image: "ubuntu:22.04", Pull: true})
	if !errors.Is(err, errNoTemplates) {
		t.Fatalf("want errNoTemplates, got %v", err)
	}

	if len(h.cmds.argvs) != 0 || len(h.imgs.calls) != 0 {
		t.Errorf("nothing should run without templates: cmds=%q images=%q", h.cmds.argvs, h.imgs.calls)
	}
}

func TestRunWith_DryRunPrintsAndDoesNothingElse(t *testing.T) {
	setupConfig(t, multiConfig)
	h := &harness{}

	err := runWith(context.Background(), h.newDeps(), &RunOptions{Image: "ubuntu:22.04", DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(h.execs.argvs) != 0 || len(h.cmds.argvs) != 0 || len(h.imgs.calls) != 0 {
		t.Errorf("dry-run must not inspect, build or exec: exec=%q cmds=%q images=%q",
			h.execs.argvs, h.cmds.argvs, h.imgs.calls)
	}
}

func TestRunWith_ImageExistsExecsWithArgv(t *testing.T) {
	setupConfig(t, multiConfig)
	h := &harness{}
	h.imgs.exists = map[string]bool{tagRun: true}

	err := runWith(context.Background(), h.newDeps(), &RunOptions{
		Image: "ubuntu:22.04", RunArgs: []string{"-it"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgvs(t, h.execs.argvs, [][]string{
		{"docker", "run", "--rm", "--volume", "/host:/container", "-it", tagRun},
	})

	if len(h.cmds.argvs) != 0 {
		t.Errorf("existing image must not trigger a build: %q", h.cmds.argvs)
	}
}

func TestRunWith_MissingImageBuildsThenExecs(t *testing.T) {
	templates := setupConfig(t, multiConfig)
	h := &harness{}

	err := runWith(context.Background(), h.newDeps(), &RunOptions{Image: "ubuntu:22.04"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgvs(t, h.cmds.argvs, [][]string{
		buildStepArgv(tagStep0, templates),
		buildStepArgv(tagStep1, templates),
	})
	assertArgvs(t, h.execs.argvs, [][]string{
		{"docker", "run", "--rm", "--volume", "/host:/container", tagRun},
	})
}

func TestRunWith_AutoBuildFailureSkipsExec(t *testing.T) {
	setupConfig(t, multiConfig)
	h := &harness{}
	h.cmds.failAt = map[int]error{0: errFake}

	err := runWith(context.Background(), h.newDeps(), &RunOptions{Image: "ubuntu:22.04"})
	if !errors.Is(err, errFake) {
		t.Fatalf("want errFake, got %v", err)
	}

	if len(h.execs.argvs) != 0 {
		t.Errorf("exec must not run after a failed auto-build: %q", h.execs.argvs)
	}
}

func TestRunWith_FeaturesMergedIntoCommand(t *testing.T) {
	setupConfig(t, multiConfig)
	h := &harness{}
	h.imgs.exists = map[string]bool{tagRun: true}

	err := runWith(context.Background(), h.newDeps(), &RunOptions{
		Image: "ubuntu:22.04", Features: []string{"extra"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertArgvs(t, h.execs.argvs, [][]string{
		{"docker", "run", "--rm", "-e", "X=1", "--volume", "/host:/container", "--volume", "/a:/a", tagRun},
	})
}

func TestCleanWith_DryRunRemovesNothing(t *testing.T) {
	h := &harness{}
	h.imgs.items = []imageapi.Summary{{ID: "sha256:a"}, {ID: "sha256:b"}}

	if err := cleanWith(context.Background(), h.newDeps(), CleanOptions{DryRun: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCalls(t, h.imgs.calls, []string{"list"})
}

func TestCleanWith_PartialRemovalFailureContinuesAndJoinsErrors(t *testing.T) {
	h := &harness{}
	h.imgs.items = []imageapi.Summary{{ID: "sha256:a"}, {ID: "sha256:b"}, {ID: "sha256:c"}}
	h.imgs.removeErr = map[string]error{"sha256:b": errFake}

	err := cleanWith(context.Background(), h.newDeps(), CleanOptions{})
	if !errors.Is(err, errFake) {
		t.Fatalf("want errFake joined into result, got %v", err)
	}

	assertCalls(t, h.imgs.calls, []string{"list", "remove sha256:a", "remove sha256:b", "remove sha256:c"})
}

func TestListNonDkglazeWith_SkipsManagedAndDanglingTags(t *testing.T) {
	h := &harness{}
	h.imgs.items = []imageapi.Summary{
		{ID: "1", RepoTags: []string{"ubuntu:22.04", danglingImageTag}},
		{ID: "2", RepoTags: []string{"mine:1"}, Labels: map[string]string{dkglazeLabel: "true"}},
		{ID: "3", RepoTags: []string{"alpine:3"}},
	}

	got, err := listNonDkglazeWith(context.Background(), h.newDeps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := []string{"ubuntu:22.04", "alpine:3"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestListWith_ListsWithoutError(t *testing.T) {
	h := &harness{}
	h.imgs.items = []imageapi.Summary{{ID: "sha256:a", RepoTags: []string{"x:1"}}}

	if err := listWith(context.Background(), h.newDeps()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCalls(t, h.imgs.calls, []string{"list"})
}
