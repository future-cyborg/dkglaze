package docker_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/future-cyborg/dkglaze/internal/config"
	"github.com/future-cyborg/dkglaze/internal/docker"
)

func TestNewImageName(t *testing.T) {
	cases := []struct {
		name          string
		image         string
		imageTemplate string
		want          string
	}{
		{
			name:          "simple image no tag",
			image:         "ubuntu",
			imageTemplate: "dkglaze",
			want:          "ubuntu-dkglaze:latest",
		},
		{
			name:          "image with tag",
			image:         "ubuntu:22.04",
			imageTemplate: "dkglaze",
			want:          "ubuntu-dkglaze:22.04",
		},
		{
			name:          "registry with port and tag",
			image:         "registry.example.com:5000/myimage:1.0",
			imageTemplate: "dkglaze",
			want:          "registry.example.com:5000/myimage-dkglaze:1.0",
		},
		{
			name:          "registry with port no tag",
			image:         "registry.example.com:5000/myimage",
			imageTemplate: "dkglaze",
			want:          "registry.example.com:5000/myimage-dkglaze:latest",
		},
		{
			name:          "registry no port with tag",
			image:         "registry.example.com/myimage:2.0",
			imageTemplate: "dkglaze",
			want:          "registry.example.com/myimage-dkglaze:2.0",
		},
		{
			name:          "digest reference",
			image:         "ubuntu@sha256:abc123def456",
			imageTemplate: "dkglaze",
			want:          "ubuntu-dkglaze:latest",
		},
		{
			name:          "registry digest reference",
			image:         "registry.example.com/myimage@sha256:abc123def456",
			imageTemplate: "dkglaze",
			want:          "registry.example.com/myimage-dkglaze:latest",
		},
		{
			name:          "tagged digest reference",
			image:         "ubuntu:22.04@sha256:abc123def456",
			imageTemplate: "dkglaze",
			want:          "ubuntu-dkglaze:22.04",
		},
		{
			name:          "registry tagged digest reference",
			image:         "registry.example.com/myimage:1.0@sha256:abc123def456",
			imageTemplate: "dkglaze",
			want:          "registry.example.com/myimage-dkglaze:1.0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := docker.ExportedNewImageName(tc.image, tc.imageTemplate)
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestRunCommand(t *testing.T) {
	t.Run("image only", func(t *testing.T) {
		got := docker.ExportedRunCommand("ubuntu:22.04")
		want := []string{"docker", "run", "ubuntu:22.04"}
		if len(got) != len(want) {
			t.Fatalf("want %v, got %v", want, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("index %d: want %q, got %q", i, want[i], got[i])
			}
		}
	})

	t.Run("with extra args", func(t *testing.T) {
		got := docker.ExportedRunCommand("ubuntu:22.04", []string{"-it", "--rm"})
		want := []string{"docker", "run", "-it", "--rm", "ubuntu:22.04"}
		if len(got) != len(want) {
			t.Fatalf("want %v, got %v", want, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("index %d: want %q, got %q", i, want[i], got[i])
			}
		}
	})

	t.Run("image name with spaces is preserved as single arg", func(t *testing.T) {
		got := docker.ExportedRunCommand("my image")
		last := got[len(got)-1]
		if last != "my image" {
			t.Errorf("expected image as single arg %q, got %q", "my image", last)
		}
	})
}

func withTempConfigDir(t *testing.T) string {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "dkglaze-docker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	t.Setenv("DKGLAZE_CONFIG_DIR", tmpDir)

	if err := os.MkdirAll(filepath.Join(tmpDir, "templates"), 0o750); err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}

	return tmpDir
}

func TestWriteTemplateFile(t *testing.T) {
	t.Run("renders template with image var", func(t *testing.T) {
		tmpDir := withTempConfigDir(t)
		templatesDir := filepath.Join(tmpDir, "templates")

		tmplContent := "FROM {{.image}}\nRUN echo hello\n"
		if err := os.WriteFile(filepath.Join(templatesDir, "base.dockerfile"), []byte(tmplContent), 0o600); err != nil {
			t.Fatalf("failed to write template: %v", err)
		}

		outFile, contextDir, err := docker.ExportedWriteTemplateFile("base", "ubuntu:22.04")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		defer os.Remove(outFile) //nolint:errcheck // best-effort cleanup of a temp file

		content, err := os.ReadFile(outFile) //nolint:gosec // path comes from our own CreateTemp call
		if err != nil {
			t.Fatalf("failed to read output file: %v", err)
		}

		if !strings.Contains(string(content), "FROM ubuntu:22.04") {
			t.Errorf("expected rendered FROM line, got:\n%s", string(content))
		}

		if contextDir != templatesDir {
			t.Errorf("expected context dir %q, got %q", templatesDir, contextDir)
		}
	})

	t.Run("uses template subdir as context when it exists", func(t *testing.T) {
		tmpDir := withTempConfigDir(t)
		templatesDir := filepath.Join(tmpDir, "templates")

		tmplContent := "FROM {{.image}}\n"
		if err := os.WriteFile(filepath.Join(templatesDir, "mytemplate.dockerfile"), []byte(tmplContent), 0o600); err != nil {
			t.Fatalf("failed to write template: %v", err)
		}

		subDir := filepath.Join(templatesDir, "mytemplate")
		if err := os.MkdirAll(subDir, 0o750); err != nil {
			t.Fatalf("failed to create subdir: %v", err)
		}

		outFile, contextDir, err := docker.ExportedWriteTemplateFile("mytemplate", "alpine:latest")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		defer os.Remove(outFile) //nolint:errcheck // best-effort cleanup of a temp file

		if contextDir != subDir {
			t.Errorf("expected context dir %q, got %q", subDir, contextDir)
		}
	})

	t.Run("returns error when template file missing", func(t *testing.T) {
		withTempConfigDir(t)

		_, _, err := docker.ExportedWriteTemplateFile("nonexistent", "ubuntu")
		if err == nil {
			t.Error("expected error for missing template, got nil")
		}
	})

	t.Run("output file is closed and readable", func(t *testing.T) {
		tmpDir := withTempConfigDir(t)
		templatesDir := filepath.Join(tmpDir, "templates")

		if err := os.WriteFile(
			filepath.Join(templatesDir, "test.dockerfile"), //nolint:gosec // path is test-controlled
			[]byte("FROM {{.image}}\n"), 0o600,
		); err != nil {
			t.Fatalf("failed to write template: %v", err)
		}

		outFile, _, err := docker.ExportedWriteTemplateFile("test", "scratch")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		defer os.Remove(outFile) //nolint:errcheck // best-effort cleanup of a temp file

		f, err := os.Open(outFile) //nolint:gosec // path comes from our own CreateTemp call
		if err != nil {
			t.Fatalf("could not open output file: %v", err)
		}

		defer f.Close() //nolint:errcheck // read-only handle; close error cannot affect the assertion

		buf := make([]byte, 64)
		n, _ := f.Read(buf)

		if n == 0 {
			t.Error("expected non-empty output file")
		}
	})
}

func TestValidateImageRef(t *testing.T) {
	valid := []string{
		"ubuntu",
		"ubuntu:22.04",
		"registry.example.com:5000/myimage:1.0",
		"registry.example.com/myimage@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
	}

	for _, img := range valid {
		t.Run("valid/"+img, func(t *testing.T) {
			if err := docker.ExportedValidateImageRef(img); err != nil {
				t.Errorf("expected nil error for %q, got %v", img, err)
			}
		})
	}

	invalid := []string{
		"",
		"; rm -rf /",
		"ubuntu&&id",
		"image name with spaces",
		"image\nwith\nnewline",
		"Ubuntu",
		"registry.example.com/myimage@sha256:abc123",
	}

	for _, img := range invalid {
		t.Run("invalid/"+img, func(t *testing.T) {
			if err := docker.ExportedValidateImageRef(img); err == nil {
				t.Errorf("expected error for %q, got nil", img)
			}
		})
	}
}

func TestBuildArgsFromResolved(t *testing.T) {
	t.Run("empty args", func(t *testing.T) {
		resolved := &config.ResolvedConfig{}
		got := docker.ExportedBuildArgsFromResolved(resolved)
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})

	t.Run("single arg", func(t *testing.T) {
		resolved := &config.ResolvedConfig{
			Build: config.Build{
				Args: []config.BuildArg{
					{Key: "FOO", Value: "bar"},
				},
			},
		}
		got := docker.ExportedBuildArgsFromResolved(resolved)
		want := []string{"--build-arg", "FOO=bar"}
		if len(got) != len(want) {
			t.Fatalf("want %v, got %v", want, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("index %d: want %q, got %q", i, want[i], got[i])
			}
		}
	})

	t.Run("multiple args", func(t *testing.T) {
		resolved := &config.ResolvedConfig{
			Build: config.Build{
				Args: []config.BuildArg{
					{Key: "FOO", Value: "bar"},
					{Key: "BAZ", Value: "qux"},
				},
			},
		}
		got := docker.ExportedBuildArgsFromResolved(resolved)
		if len(got) != 4 {
			t.Fatalf("expected 4 elements, got %d: %v", len(got), got)
		}
		if got[0] != "--build-arg" || got[1] != "FOO=bar" {
			t.Errorf("first arg wrong: %v", got[:2])
		}
		if got[2] != "--build-arg" || got[3] != "BAZ=qux" {
			t.Errorf("second arg wrong: %v", got[2:])
		}
	})
}

func TestMountFlagsFromResolved(t *testing.T) {
	t.Run("no mounts", func(t *testing.T) {
		resolved := &config.ResolvedConfig{}
		got := docker.ExportedMountFlagsFromResolved(resolved)
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})

	t.Run("single mount", func(t *testing.T) {
		resolved := &config.ResolvedConfig{
			Run: config.Run{
				Mounts: []string{"/host:/container"},
			},
		}
		got := docker.ExportedMountFlagsFromResolved(resolved)
		want := []string{"--volume", "/host:/container"}
		if len(got) != len(want) {
			t.Fatalf("want %v, got %v", want, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("index %d: want %q, got %q", i, want[i], got[i])
			}
		}
	})

	t.Run("multiple mounts", func(t *testing.T) {
		resolved := &config.ResolvedConfig{
			Run: config.Run{
				Mounts: []string{"/a:/a", "/b:/b:ro"},
			},
		}
		got := docker.ExportedMountFlagsFromResolved(resolved)
		if len(got) != 4 {
			t.Fatalf("expected 4 elements, got %d: %v", len(got), got)
		}
		if got[0] != "--volume" || got[1] != "/a:/a" {
			t.Errorf("first mount wrong: %v", got[:2])
		}
		if got[2] != "--volume" || got[3] != "/b:/b:ro" {
			t.Errorf("second mount wrong: %v", got[2:])
		}
	})
}

const testLocalPath = "/project/.dkglaze.yml"

// assertWarnings checks that got has exactly one entry per wanted substring, in order.
func assertWarnings(t *testing.T, got []string, wantSubstrings ...string) {
	t.Helper()

	if len(got) != len(wantSubstrings) {
		t.Fatalf("want %d warning(s) %q, got %d: %q", len(wantSubstrings), wantSubstrings, len(got), got)
	}

	for i, sub := range wantSubstrings {
		if !strings.Contains(got[i], sub) {
			t.Errorf("warning %d: want it to contain %q, got %q", i, sub, got[i])
		}
	}
}

func TestWarnLocalProvenance_NoWarningWhenGlobal(t *testing.T) {
	// Nothing came from the local config, so there is nothing to warn about.
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base": {Run: config.Run{Args: []string{"--rm"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	prov := &config.LocalProvenance{
		Features: map[string]bool{},
		Profiles: map[string]bool{},
	}

	got := docker.ExportedLocalProvenanceWarnings(conf, prov, "default", true, testLocalPath)
	assertWarnings(t, got)
}

func TestWarnLocalProvenance_WarnsWhenProfileFromLocal(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base": {Run: config.Run{Args: []string{"--rm"}}},
		},
		Profiles: map[string]config.Profile{
			"local-profile": {Features: []string{"base"}},
		},
	}

	prov := &config.LocalProvenance{
		Features: map[string]bool{},
		Profiles: map[string]bool{"local-profile": true},
	}

	got := docker.ExportedLocalProvenanceWarnings(conf, prov, "local-profile", true, testLocalPath)
	assertWarnings(t, got, `using profile "local-profile" from local config `+testLocalPath)
}

func TestWarnLocalProvenance_WarnsWhenFeatureFromLocal(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base":          {Run: config.Run{Args: []string{"--rm"}}},
			"local-feature": {Run: config.Run{Mounts: []string{"/local:/local"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base", "local-feature"}},
		},
	}

	prov := &config.LocalProvenance{
		Features: map[string]bool{"local-feature": true},
		Profiles: map[string]bool{},
	}

	got := docker.ExportedLocalProvenanceWarnings(conf, prov, "default", true, testLocalPath)
	assertWarnings(t, got, `feature "local-feature" from local config `+testLocalPath)
}

func TestWarnLocalProvenance_IgnoresUnknownProfile(t *testing.T) {
	// An unknown profile has no chain or features to inspect, so the local flag is unreachable.
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base": {Run: config.Run{Args: []string{"--rm"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	prov := &config.LocalProvenance{
		Features: map[string]bool{},
		Profiles: map[string]bool{"nonexistent": true},
	}

	got := docker.ExportedLocalProvenanceWarnings(conf, prov, "nonexistent", true, testLocalPath)
	assertWarnings(t, got)
}

func TestWarnLocalProvenance_WarnsWhenDefaultFromLocalAndProfileNotExplicit(t *testing.T) {
	conf := &config.Config{
		Defaults: config.Defaults{Profile: "default"},
		Features: map[string]config.Feature{
			"base": {Run: config.Run{Args: []string{"--rm"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	prov := &config.LocalProvenance{
		Features:         map[string]bool{},
		Profiles:         map[string]bool{},
		DefaultFromLocal: true,
	}

	// The default-selection warning fires because the profile was not passed via -p.
	got := docker.ExportedLocalProvenanceWarnings(conf, prov, "default", false, testLocalPath)
	assertWarnings(t, got, `profile "default" selected by defaults.profile in local config `+testLocalPath)
}

func TestWarnLocalProvenance_NoDefaultWarningWhenProfileExplicit(t *testing.T) {
	conf := &config.Config{
		Features: map[string]config.Feature{
			"base": {Run: config.Run{Args: []string{"--rm"}}},
		},
		Profiles: map[string]config.Profile{
			"default": {Features: []string{"base"}},
		},
	}

	prov := &config.LocalProvenance{
		Features:         map[string]bool{},
		Profiles:         map[string]bool{},
		DefaultFromLocal: true,
	}

	// The default-selection warning does not apply since the profile was passed via -p.
	got := docker.ExportedLocalProvenanceWarnings(conf, prov, "default", true, testLocalPath)
	assertWarnings(t, got)
}
