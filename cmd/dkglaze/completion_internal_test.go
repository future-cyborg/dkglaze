package main

import (
	"io"
	"strings"
	"testing"
)

// completionFor generates the completion script for shell from the real Cli
// model, the same way `dkglaze completion <shell>` does.
func completionFor(t *testing.T, shell string) string {
	t.Helper()

	parser := newParser(t, &Cli{}, io.Discard, func(int) {})

	out, err := generateCompletion(shell, parser.Model)
	if err != nil {
		t.Fatalf("generateCompletion(%q): %v", shell, err)
	}

	return out
}

func TestGenerateCompletionBash(t *testing.T) {
	out := completionFor(t, shellBash)

	if out == "" {
		t.Fatal("bash completion is empty")
	}

	for _, want := range []string{
		"_dkglaze_completions() {",
		"complete -F _dkglaze_completions dkglaze",
		// Hidden helpers that the generated script shells out to.
		`"${COMP_WORDS[0]}" _list-images`,
		`"${COMP_WORDS[0]}" _list-profiles`,
		`"${COMP_WORDS[0]}" _list-features`,
		// Profile and feature flags complete from the helpers above.
		"--profile|-p)",
		"--feature|-f)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("bash completion missing %q", want)
		}
	}
}

func TestGenerateCompletionZsh(t *testing.T) {
	out := completionFor(t, shellZsh)

	if out == "" {
		t.Fatal("zsh completion is empty")
	}

	for _, want := range []string{
		"#compdef dkglaze",
		"_dkglaze() {",
		`_dkglaze "$@"`,
		`${words[1]} _list-images`,
		`${words[1]} _list-profiles`,
		`${words[1]} _list-features`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("zsh completion missing %q", want)
		}
	}
}

func TestGenerateCompletionListsVisibleSubcommands(t *testing.T) {
	for _, shell := range []string{shellBash, shellZsh} {
		t.Run(shell, func(t *testing.T) {
			out := completionFor(t, shell)

			// Every visible top-level command must appear in the script.
			for _, name := range []string{"build", "run", "clean", "list", "config", "completion"} {
				if !strings.Contains(out, name) {
					t.Errorf("%s completion missing command %q", shell, name)
				}
			}
		})
	}
}

func TestGenerateCompletionUnknownShell(t *testing.T) {
	parser := newParser(t, &Cli{}, io.Discard, func(int) {})

	out, err := generateCompletion("fish", parser.Model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out != "" {
		t.Errorf("expected empty output for unsupported shell, got %q", out)
	}
}

func TestCollectCmdMeta(t *testing.T) {
	parser := newParser(t, &Cli{}, io.Discard, func(int) {})
	cmds := collectCmdMeta(parser.Model.Node)

	byName := map[string]cmdMeta{}
	for _, c := range cmds {
		byName[c.Name] = c
	}

	if _, ok := byName["_list-images"]; ok {
		t.Error("hidden command _list-images must not appear in completion metadata")
	}

	build, ok := byName["build"]
	if !ok {
		t.Fatal("build missing from command metadata")
	}

	if build.PosCompl != posComplImages {
		t.Errorf("build PosCompl = %q, want %q", build.PosCompl, posComplImages)
	}

	if !hasFlag(build.Flags, "profile", complProfiles) {
		t.Errorf("build flags missing profile with profiles completion: %+v", build.Flags)
	}

	completion, ok := byName["completion"]
	if !ok {
		t.Fatal("completion missing from command metadata")
	}

	if completion.PosCompl != "enum:bash,zsh" {
		t.Errorf("completion PosCompl = %q, want enum:bash,zsh", completion.PosCompl)
	}

	cfg, ok := byName["config"]
	if !ok {
		t.Fatal("config missing from command metadata")
	}

	list := findChild(t, cfg.Children, "list")
	if got := childNames(list.Children); strings.Join(got, " ") != "profiles features" {
		t.Errorf("config list children = %v, want [profiles features]", got)
	}
}

func hasFlag(flags []flagMeta, long, completion string) bool {
	for _, f := range flags {
		if f.Long == long && f.Completion == completion {
			return true
		}
	}

	return false
}

func findChild(t *testing.T, children []cmdMeta, name string) cmdMeta {
	t.Helper()

	for _, c := range children {
		if c.Name == name {
			return c
		}
	}

	t.Fatalf("child %q not found in %v", name, childNames(children))

	return cmdMeta{}
}

func TestFlagWords(t *testing.T) {
	cases := []struct {
		name  string
		flags []flagMeta
		want  string
	}{
		{"none", nil, ""},
		{"long only", []flagMeta{{Long: "pull"}}, "--pull"},
		{"long and short", []flagMeta{{Long: "profile", Short: 'p'}}, "--profile -p"},
		{"mixed", []flagMeta{{Long: "profile", Short: 'p'}, {Long: "pull"}}, "--profile -p --pull"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := flagWords(tc.flags); got != tc.want {
				t.Errorf("flagWords() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnumVals(t *testing.T) {
	cases := []struct {
		posCompl string
		want     string
	}{
		{"enum:bash,zsh", "bash zsh"},
		{"enum:bash", "bash"},
		{"", ""},
	}

	for _, tc := range cases {
		if got := enumVals(tc.posCompl); got != tc.want {
			t.Errorf("enumVals(%q) = %q, want %q", tc.posCompl, got, tc.want)
		}
	}
}

func TestCollectFlagPatterns(t *testing.T) {
	cmds := []cmdMeta{
		{Name: "build", Flags: []flagMeta{
			{Long: "profile", Short: 'p', Completion: complProfiles},
			{Long: "pull", IsBool: true},
		}},
		{Name: "run", Flags: []flagMeta{
			{Long: "profile", Short: 'p', Completion: complProfiles},
			{Long: "feature", Short: 'f', Completion: complFeatures},
			{Long: "dry-run", Short: 'n', IsBool: true},
		}},
		{Name: "clean", Flags: []flagMeta{
			{Long: "output", Short: 'o'},
		}},
	}

	t.Run("profiles dedupe across commands and skip bool flags", func(t *testing.T) {
		got := collectFlagPatterns(cmds, func(f flagMeta) bool { return f.Completion == complProfiles })
		if strings.Join(got, " ") != "--profile -p" {
			t.Errorf("got %v, want [--profile -p]", got)
		}
	})

	t.Run("feature flag patterns", func(t *testing.T) {
		got := collectFlagPatterns(cmds, func(f flagMeta) bool { return f.Completion == complFeatures })
		if strings.Join(got, " ") != "--feature -f" {
			t.Errorf("got %v, want [--feature -f]", got)
		}
	})

	t.Run("value flags without completion, bool flags excluded", func(t *testing.T) {
		got := collectFlagPatterns(cmds, func(f flagMeta) bool { return f.Completion == "" })
		if strings.Join(got, " ") != "--output -o" {
			t.Errorf("got %v, want [--output -o]", got)
		}
	})
}

func TestChildNames(t *testing.T) {
	if got := childNames(nil); len(got) != 0 {
		t.Errorf("childNames(nil) = %v, want empty", got)
	}

	got := childNames([]cmdMeta{{Name: "a"}, {Name: "b"}})
	if strings.Join(got, " ") != "a b" {
		t.Errorf("childNames = %v, want [a b]", got)
	}
}
