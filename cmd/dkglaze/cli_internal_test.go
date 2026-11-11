package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// Literals shared with non-test code: goconst counts identical literals across
// the whole package, so the test refers to the production constants instead of
// repeating the strings.
const (
	testVersion    = "1.2.3"
	kongVersionKey = "version"
	devVersion     = "dev"
)

// newParser builds a kong parser for Cli without parsing anything. The exit
// hook is replaced so --help/--version cannot terminate the test binary, and
// the version variable is pinned so --version output is deterministic.
func newParser(t *testing.T, cli *Cli, stdout io.Writer, exit func(int)) *kong.Kong {
	t.Helper()

	parser, err := kong.New(cli,
		kong.Name("dkglaze"),
		kong.Exit(exit),
		kong.Writers(stdout, io.Discard),
		kong.Vars{kongVersionKey: testVersion},
	)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}

	return parser
}

// parseArgs parses args against a fresh Cli. It never runs a command, so no
// Docker daemon is needed.
func parseArgs(t *testing.T, args ...string) (*Cli, *kong.Context, error) {
	t.Helper()

	cli := &Cli{}
	parser := newParser(t, cli, io.Discard, func(int) {})

	ctx, err := parser.Parse(args)
	if err != nil {
		return cli, ctx, fmt.Errorf("parse %v: %w", args, err)
	}

	return cli, ctx, nil
}

// commandPath returns the selected command as space-separated names,
// e.g. "config set default". The root application name is omitted.
func commandPath(ctx *kong.Context) string {
	var names []string

	for n := ctx.Selected(); n != nil && n.Parent != nil; n = n.Parent {
		names = append([]string{n.Name}, names...)
	}

	return strings.Join(names, " ")
}

func TestParseSelectsCommand(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantPath string
	}{
		{"build", []string{"build", "alpine"}, "build"},
		{"run", []string{"run", "alpine"}, "run"},
		{"clean", []string{"clean"}, "clean"},
		{"list", []string{"list"}, "list"},
		{"completion bash", []string{"completion", shellBash}, "completion"},
		{"completion zsh", []string{"completion", shellZsh}, "completion"},
		{"config path", []string{"config", "path"}, "config path"},
		{"config init", []string{"config", "init"}, "config init"},
		{"config validate", []string{"config", "validate"}, "config validate"},
		{"config edit", []string{"config", "edit"}, "config edit"},
		{"config show", []string{"config", "show"}, "config show"},
		{"config list profiles", []string{"config", "list", "profiles"}, "config list profiles"},
		{"config list features", []string{"config", "list", "features"}, "config list features"},
		{"config set default", []string{"config", "set", "default", "prod"}, "config set default"},
		{"hidden _list-images", []string{"_list-images"}, "_list-images"},
		{"hidden _list-profiles", []string{"_list-profiles"}, "_list-profiles"},
		{"hidden _list-features", []string{"_list-features"}, "_list-features"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ctx, err := parseArgs(t, tc.args...)
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}

			if got := commandPath(ctx); got != tc.wantPath {
				t.Errorf("selected command = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

func TestParseBuildFlags(t *testing.T) {
	t.Run("image only leaves flags at zero values", func(t *testing.T) {
		cli, _, err := parseArgs(t, "build", "alpine")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if cli.Build.Image != "alpine" {
			t.Errorf("Image = %q, want alpine", cli.Build.Image)
		}

		if len(cli.Build.BuildArgs) != 0 || cli.Build.Profile != "" ||
			cli.Build.Pull || cli.Build.DryRun || cli.Build.TrustLocalShell {
			t.Errorf("expected zero-value flags, got %+v", cli.Build)
		}
	})

	t.Run("all flags populate", func(t *testing.T) {
		cli, _, err := parseArgs(t, "build", "alpine", "-p", "staging", "--pull", "-n", "--trust")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if cli.Build.Profile != "staging" {
			t.Errorf("Profile = %q, want staging", cli.Build.Profile)
		}

		if !cli.Build.Pull {
			t.Error("Pull = false, want true")
		}

		if !cli.Build.DryRun {
			t.Error("DryRun = false, want true")
		}

		if !cli.Build.TrustLocalShell {
			t.Error("TrustLocalShell = false, want true")
		}
	})

	t.Run("args after -- are passed through as build-args", func(t *testing.T) {
		cli, _, err := parseArgs(t, "build", "alpine", "--", "--no-cache", "--build-arg", "X=1")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		want := []string{"--no-cache", "--build-arg", "X=1"}
		if strings.Join(cli.Build.BuildArgs, " ") != strings.Join(want, " ") {
			t.Errorf("BuildArgs = %q, want %q", cli.Build.BuildArgs, want)
		}
	})
}

func TestParseRunFlags(t *testing.T) {
	t.Run("repeatable feature flag accumulates", func(t *testing.T) {
		cli, _, err := parseArgs(t, "run", "alpine", "-f", "a", "--feature", "b")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if strings.Join(cli.Run.Features, ",") != "a,b" {
			t.Errorf("Features = %q, want [a b]", cli.Run.Features)
		}
	})

	t.Run("profile, dry-run and trust populate", func(t *testing.T) {
		cli, _, err := parseArgs(t, "run", "alpine", "--profile", "prod", "-n", "--trust")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if cli.Run.Profile != "prod" {
			t.Errorf("Profile = %q, want prod", cli.Run.Profile)
		}

		if !cli.Run.DryRun {
			t.Error("DryRun = false, want true")
		}

		if !cli.Run.TrustLocalShell {
			t.Error("TrustLocalShell = false, want true")
		}
	})

	t.Run("args after -- are passed through as run args", func(t *testing.T) {
		cli, _, err := parseArgs(t, "run", "alpine", "--", "-it", "--rm")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if cli.Run.Image != "alpine" {
			t.Errorf("Image = %q, want alpine", cli.Run.Image)
		}

		if strings.Join(cli.Run.RunArgs, " ") != "-it --rm" {
			t.Errorf("RunArgs = %q, want [-it --rm]", cli.Run.RunArgs)
		}
	})
}

func TestParseCleanAndGlobalFlags(t *testing.T) {
	t.Run("clean dry-run short flag", func(t *testing.T) {
		cli, _, err := parseArgs(t, "clean", "-n")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if !cli.Clean.DryRun {
			t.Error("Clean.DryRun = false, want true")
		}
	})

	t.Run("clean defaults to not dry-run", func(t *testing.T) {
		cli, _, err := parseArgs(t, "clean")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if cli.Clean.DryRun {
			t.Error("Clean.DryRun = true, want false")
		}
	})

	t.Run("verbose is global and applies before a subcommand", func(t *testing.T) {
		cli, _, err := parseArgs(t, "--verbose", "list")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if !cli.Verbose {
			t.Error("Verbose = false, want true")
		}
	})

	t.Run("config show --resolved", func(t *testing.T) {
		cli, _, err := parseArgs(t, "config", "show", "--resolved")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if !cli.Config.Show.Resolved {
			t.Error("Config.Show.Resolved = false, want true")
		}
	})

	t.Run("config set default captures profile", func(t *testing.T) {
		cli, _, err := parseArgs(t, "config", "set", "default", "prod")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if cli.Config.Set.Default.Profile != "prod" {
			t.Errorf("Profile = %q, want prod", cli.Config.Set.Default.Profile)
		}
	})
}

func TestParseCompletionShell(t *testing.T) {
	for _, shell := range []string{shellBash, shellZsh} {
		t.Run(shell, func(t *testing.T) {
			cli, _, err := parseArgs(t, "completion", shell)
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}

			if cli.Completion.Shell != shell {
				t.Errorf("Shell = %q, want %q", cli.Completion.Shell, shell)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"build without image", []string{"build"}},
		{"run without image", []string{"run"}},
		{"completion without shell", []string{"completion"}},
		{"completion with unsupported shell", []string{"completion", "fish"}},
		{"config set default without profile", []string{"config", "set", "default"}},
		{"unknown command", []string{"frobnicate"}},
		{"unknown flag", []string{"clean", "--no-such-flag"}},
		{"flag missing its value", []string{"build", "alpine", "-p"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseArgs(t, tc.args...); err == nil {
				t.Errorf("expected parse error for %v, got nil", tc.args)
			}
		})
	}
}

func TestVersionFlagPrintsInjectedVersion(t *testing.T) {
	var stdout bytes.Buffer

	exitCode := -1

	parser := newParser(t, &Cli{}, &stdout, func(code int) { exitCode = code })

	// The version flag exits via the hook, which is a no-op here, so the
	// remaining parse may report a missing command. Only stdout/exit matter.
	_, _ = parser.Parse([]string{"--version"})

	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}

	if got := strings.TrimSpace(stdout.String()); got != testVersion {
		t.Errorf("version output = %q, want %s", got, testVersion)
	}
}
