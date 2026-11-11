package docker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/future-cyborg/dkglaze/internal/config"
)

func assertPlan(t *testing.T, got, want *buildPlan) {
	t.Helper()

	if got.Image != want.Image || got.FinalTag != want.FinalTag {
		t.Fatalf("image/final: got (%q, %q), want (%q, %q)", got.Image, got.FinalTag, want.Image, want.FinalTag)
	}

	if len(got.Steps) != len(want.Steps) {
		t.Fatalf("got %d steps, want %d: %+v", len(got.Steps), len(want.Steps), got.Steps)
	}

	for i := range want.Steps {
		g, w := got.Steps[i], want.Steps[i]
		if g.Template != w.Template || g.FromTag != w.FromTag || g.ResultTag != w.ResultTag ||
			!slices.Equal(g.Args, w.Args) {
			t.Errorf("step %d:\n got:  %+v\n want: %+v", i, g, w)
		}
	}
}

func TestPlanBuild(t *testing.T) {
	cases := []struct {
		name     string
		image    string
		resolved *config.ResolvedConfig
		userArgs []string
		want     *buildPlan
	}{
		{
			name:  "two templates chain from base image through intermediate tags",
			image: "ubuntu:22.04",
			resolved: &config.ResolvedConfig{
				Suffix: "dev",
				Build: config.Build{
					Templates: []string{"a", "b"},
					Args:      []config.BuildArg{{Key: "FOO", Value: "bar"}},
				},
			},
			userArgs: []string{"--no-cache"},
			want: &buildPlan{
				Image:    "ubuntu:22.04",
				FinalTag: "ubuntu-dev:22.04",
				Steps: []buildStep{
					{
						Template: "a", FromTag: "ubuntu:22.04", ResultTag: "ubuntu-dev.0:22.04",
						Args: []string{"--build-arg", "FOO=bar", "--no-cache"},
					},
					{
						Template: "b", FromTag: "ubuntu-dev.0:22.04", ResultTag: "ubuntu-dev.1:22.04",
						Args: []string{"--build-arg", "FOO=bar", "--no-cache"},
					},
				},
			},
		},
		{
			name:     "digest reference drops the digest and defaults to latest",
			image:    "ubuntu@sha256:abc123",
			resolved: &config.ResolvedConfig{Suffix: "dev", Build: config.Build{Templates: []string{"a"}}},
			want: &buildPlan{
				Image:    "ubuntu@sha256:abc123",
				FinalTag: "ubuntu-dev:latest",
				Steps: []buildStep{
					{Template: "a", FromTag: "ubuntu@sha256:abc123", ResultTag: "ubuntu-dev.0:latest"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := planBuild(tc.image, tc.resolved, tc.userArgs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			assertPlan(t, got, tc.want)
		})
	}
}

func TestPlanBuild_NoTemplatesIsError(t *testing.T) {
	_, err := planBuild("ubuntu", &config.ResolvedConfig{Suffix: "dev"}, nil)
	if !errors.Is(err, errNoTemplates) {
		t.Fatalf("want errNoTemplates, got %v", err)
	}
}

func TestBuildStepCommand_DisplayStrings(t *testing.T) {
	cases := []struct {
		name string
		step buildStep
		want string
	}{
		{
			name: "with build args and user args",
			step: buildStep{
				Template: "a", ResultTag: "ubuntu-dev.0:22.04",
				Args: []string{"--build-arg", "FOO=bar", "--no-cache"},
			},
			want: "docker build --label com.dkglaze=true -t ubuntu-dev.0:22.04 -f <a.dockerfile> " +
				"--build-arg FOO=bar --no-cache <context>",
		},
		{
			name: "no extra args",
			step: buildStep{Template: "base", ResultTag: "img-x.0:latest"},
			want: "docker build --label com.dkglaze=true -t img-x.0:latest -f <base.dockerfile> <context>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(tc.step.displayCommand(), " "); got != tc.want {
				t.Errorf("display:\n got:  %q\n want: %q", got, tc.want)
			}
		})
	}
}

func TestBuildStepCommand_ExecutionUsesRealPaths(t *testing.T) {
	step := buildStep{
		Template: "a", ResultTag: "ubuntu-dev.0:22.04",
		Args: []string{"--build-arg", "FOO=bar"},
	}

	got := strings.Join(step.command("/tmp/a.dockerfile", "/cfg/templates"), " ")
	want := "docker build --label com.dkglaze=true -t ubuntu-dev.0:22.04 -f /tmp/a.dockerfile " +
		"--build-arg FOO=bar /cfg/templates"

	if got != want {
		t.Errorf("execution:\n got:  %q\n want: %q", got, want)
	}

	// The step's own Args must not be mutated by building the argv.
	if !slices.Equal(step.Args, []string{"--build-arg", "FOO=bar"}) {
		t.Errorf("step.Args mutated: %q", step.Args)
	}
}

func TestSelectProfile(t *testing.T) {
	conf := &config.Config{Defaults: config.Defaults{Profile: "default"}}

	cases := []struct {
		name      string
		requested string
		want      profileSelection
	}{
		{name: "defaults when not requested", requested: "", want: profileSelection{Active: "default"}},
		{
			name: "requested overrides default", requested: "other",
			want: profileSelection{Requested: "other", Active: "other"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selectProfile(conf, tc.requested)
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}

			if got.explicit() != (tc.requested != "") {
				t.Errorf("explicit() = %t, want %t", got.explicit(), tc.requested != "")
			}
		})
	}
}

func TestPlanRun(t *testing.T) {
	resolved := &config.ResolvedConfig{
		Suffix: "dev",
		Run:    config.Run{Args: []string{"--rm"}, Mounts: []string{"/a:/a"}},
	}

	got := planRun("ubuntu:22.04", resolved, []string{"-it"})

	if got.Image != "ubuntu-dev:22.04" {
		t.Errorf("image: got %q, want %q", got.Image, "ubuntu-dev:22.04")
	}

	want := []string{"docker", "run", "--rm", "--volume", "/a:/a", "-it", "ubuntu-dev:22.04"}
	if !slices.Equal(got.Command, want) {
		t.Errorf("command:\n got:  %q\n want: %q", got.Command, want)
	}
}

func TestApplyRunFeatures_CollectsArgsAndWarnings(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"withbuild": {
				Build: config.Build{Templates: []string{"a"}},
				Run:   config.Run{Args: []string{"-x"}, Mounts: []string{"/m:/m"}},
			},
		},
	}
	prov := &config.LocalProvenance{Features: map[string]bool{"withbuild": true}}

	extra, warnings, err := applyRunFeatures(context.Background(), conf, prov, &RunOptions{
		Features: []string{"withbuild"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Equal(extra.Args, []string{"-x"}) || !slices.Equal(extra.Mounts, []string{"/m:/m"}) {
		t.Errorf("extra: got %+v", extra)
	}

	if len(warnings) != 2 ||
		!strings.Contains(warnings[0], "has build config") ||
		!strings.Contains(warnings[1], `"withbuild" from local config`) {
		t.Errorf("warnings: got %q", warnings)
	}
}

func TestApplyRunFeatures_UnknownFeatureIsError(t *testing.T) {
	conf := &config.Config{}

	_, _, err := applyRunFeatures(context.Background(), conf, &config.LocalProvenance{}, &RunOptions{
		Features: []string{"missing"},
	})
	if err == nil {
		t.Fatal("expected error for unknown feature, got nil")
	}
}
