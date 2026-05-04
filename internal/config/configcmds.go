package config

import (
	goerrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/future-cyborg/udocker/internal/errors"
)

var (
	errEmptyDocument   = goerrors.New("empty config document")
	errMissingDefaults = goerrors.New("missing 'defaults' key in config")
	errMissingProfile  = goerrors.New("missing 'profile' key under 'defaults'")
)

// ListProfiles returns a formatted string listing all profiles with the default marked.
func ListProfiles(cfg *Config) string {
	names := sortedProfileNames(cfg)

	var sb strings.Builder

	for _, name := range names {
		profile := cfg.Profiles[name]
		label := name
		if name == cfg.Defaults.Profile {
			label = name + " *"
		}

		fmt.Fprintf(&sb, "%-22s [%s]\n", label, strings.Join(profile.Features, ", "))
	}

	return sb.String()
}

// ListFeatures returns a formatted string summarizing all features with counts.
func ListFeatures(cfg *Config) string {
	names := sortedFeatureNames(cfg)

	var sb strings.Builder

	for _, name := range names {
		f := cfg.Features[name]
		fmt.Fprintf(&sb, "%-20s templates=%-3d build_args=%-3d run_args=%-3d mounts=%d\n",
			name,
			len(f.Build.Templates),
			len(f.Build.Args),
			len(f.Run.Args),
			len(f.Run.Mounts),
		)
	}

	return sb.String()
}

// SetDefaultProfile updates defaults.profile in the config file at cfgPath in-place,
// preserving comments and key ordering. Writes atomically via temp file + rename.
func SetDefaultProfile(cfgPath, profile string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	if _, ok := cfg.Profiles[profile]; !ok {
		return errors.Wrap(fmt.Errorf("%w: %q", errUnknownProfile, profile))
	}

	return setDefaultInFile(cfgPath, profile)
}

// ShowResolved resolves all profiles and returns a formatted string of the computed config.
func ShowResolved(cfg *Config) (string, error) {
	names := sortedProfileNames(cfg)

	var sb strings.Builder

	for _, name := range names {
		resolved, err := cfg.ResolveProfile(name)
		if err != nil {
			return "", err
		}

		writeResolvedProfile(&sb, name, resolved)
	}

	return sb.String(), nil
}

func setDefaultInFile(cfgPath, profile string) error {
	//nolint:gosec // cfgPath is resolved from GetConfigPaths, not user input
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return errors.Wrap(err)
	}

	var doc yaml.Node

	if err := yaml.Unmarshal(data, &doc); err != nil {
		return errors.Wrap(err)
	}

	if err := setProfileNode(&doc, profile); err != nil {
		return err
	}

	return writeAtomically(cfgPath, &doc)
}

func setProfileNode(doc *yaml.Node, profile string) error {
	if len(doc.Content) == 0 {
		return errors.Wrap(errEmptyDocument)
	}

	defaultsNode := findMappingValue(doc.Content[0], "defaults")
	if defaultsNode == nil {
		return errors.Wrap(errMissingDefaults)
	}

	profileNode := findMappingValue(defaultsNode, "profile")
	if profileNode == nil {
		return errors.Wrap(errMissingProfile)
	}

	profileNode.Value = profile

	return nil
}

func findMappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}

func writeAtomically(path string, node *yaml.Node) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yml.tmp")
	if err != nil {
		return errors.Wrap(err)
	}

	tmpName := tmp.Name()
	enc := yaml.NewEncoder(tmp)
	enc.SetIndent(2)

	if err = enc.Encode(node); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)

		return errors.Wrap(err)
	}

	if err = tmp.Close(); err != nil {
		_ = os.Remove(tmpName)

		return errors.Wrap(err)
	}

	return errors.Wrap(os.Rename(tmpName, path))
}

func writeResolvedProfile(sb *strings.Builder, name string, r *ResolvedConfig) {
	fmt.Fprintf(sb, "Profile: %s\n", name)

	args := make([]string, len(r.Build.Args))
	for i, a := range r.Build.Args {
		args[i] = a.Key + "=" + a.Value
	}

	writeResolvedList(sb, "Templates", r.Build.Templates)
	writeResolvedList(sb, "Build Args", args)
	writeResolvedList(sb, "Run Args", r.Run.Args)
	writeResolvedList(sb, "Mounts", r.Run.Mounts)
	fmt.Fprintln(sb)
}

func writeResolvedList(sb *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}

	fmt.Fprintf(sb, "  %-12s %s\n", label+":", items[0])

	for _, item := range items[1:] {
		fmt.Fprintf(sb, "  %-12s %s\n", "", item)
	}
}

func sortedProfileNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Profiles))

	for name := range cfg.Profiles {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

func sortedFeatureNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Features))

	for name := range cfg.Features {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}
