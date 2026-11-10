package main

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/alecthomas/kong"

	"github.com/future-cyborg/ldocker/internal/errors"
)

const posComplImages = "images"

//go:embed bash.tmpl
var bashTmpl string

//go:embed zsh.tmpl
var zshTmpl string

type cmdMeta struct {
	Name     string
	Flags    []flagMeta
	PosCompl string // "images", "enum:bash,zsh", or ""
	Children []cmdMeta
}

type flagMeta struct {
	Long       string
	Short      rune
	IsBool     bool
	Completion string
}

type completionData struct {
	AppName             string
	FnName              string
	Cmds                []cmdMeta
	ValueFlagPatterns   []string
	ProfileFlagPatterns []string
	FeatureFlagPatterns []string
}

// --- model collection ---

func flagMetaFromNode(node *kong.Node) []flagMeta {
	var out []flagMeta

	for _, f := range node.Flags {
		if !f.Hidden {
			out = append(out, flagMeta{
				Long:       f.Name,
				Short:      f.Short,
				IsBool:     f.IsBool(),
				Completion: f.Tag.Get("completion"),
			})
		}
	}

	return out
}

func posComplFromNode(node *kong.Node) string {
	if len(node.Positional) == 0 {
		return ""
	}

	pos := node.Positional[0]

	if c := pos.Tag.Get("completion"); c != "" {
		return c
	}

	if pos.Enum != "" {
		return "enum:" + pos.Enum
	}

	return ""
}

func collectCmdMeta(node *kong.Node) []cmdMeta {
	var out []cmdMeta

	for _, child := range node.Children {
		if child.Hidden {
			continue
		}

		m := cmdMeta{
			Name:     child.Name,
			Flags:    flagMetaFromNode(child),
			PosCompl: posComplFromNode(child),
		}

		if len(child.Children) > 0 {
			m.Children = collectCmdMeta(child)
		}

		out = append(out, m)
	}

	return out
}

// --- helpers ---

func enumVals(posCompl string) string {
	return strings.ReplaceAll(strings.TrimPrefix(posCompl, "enum:"), ",", " ")
}

func flagWords(flags []flagMeta) string {
	var words []string

	for _, f := range flags {
		words = append(words, "--"+f.Long)

		if f.Short != 0 {
			words = append(words, "-"+string(f.Short))
		}
	}

	return strings.Join(words, " ")
}

func childNames(children []cmdMeta) []string {
	names := make([]string, len(children))

	for i, c := range children {
		names[i] = c.Name
	}

	return names
}

func addFlagPatterns(f flagMeta, seen map[string]bool, pats *[]string) {
	if f.IsBool {
		return
	}

	long := "--" + f.Long
	if !seen[long] {
		*pats = append(*pats, long)
		seen[long] = true
	}

	if f.Short != 0 {
		short := "-" + string(f.Short)
		if !seen[short] {
			*pats = append(*pats, short)
			seen[short] = true
		}
	}
}

func collectFlagPatterns(cmds []cmdMeta, match func(flagMeta) bool) []string {
	var pats []string

	seen := map[string]bool{}

	for _, c := range cmds {
		for _, f := range c.Flags {
			if match(f) {
				addFlagPatterns(f, seen, &pats)
			}
		}
	}

	return pats
}

// --- entry point ---

func generateCompletion(shell string, app *kong.Application) (string, error) {
	cmds := collectCmdMeta(app.Node)

	var tmplStr string

	switch shell {
	case "bash":
		tmplStr = bashTmpl
	case "zsh":
		tmplStr = zshTmpl
	default:
		return "", nil
	}

	fnMap := template.FuncMap{
		"isImages":   func(s string) bool { return s == posComplImages },
		"isEnum":     func(s string) bool { return strings.HasPrefix(s, "enum:") },
		"enumVals":   enumVals,
		"flagWords":  flagWords,
		"childNames": childNames,
		"anyHasFlags": func(children []cmdMeta) bool {
			for _, c := range children {
				if len(c.Flags) > 0 {
					return true
				}
			}

			return false
		},
		"join": func(sep string, items []string) string { return strings.Join(items, sep) },
	}

	fnName := "_" + strings.ReplaceAll(app.Name, "-", "_") + "_completions"
	data := completionData{
		AppName:             app.Name,
		FnName:              fnName,
		Cmds:                cmds,
		ValueFlagPatterns:   collectFlagPatterns(cmds, func(f flagMeta) bool { return f.Completion == "" }),
		ProfileFlagPatterns: collectFlagPatterns(cmds, func(f flagMeta) bool { return f.Completion == "profiles" }),
		FeatureFlagPatterns: collectFlagPatterns(cmds, func(f flagMeta) bool { return f.Completion == "features" }),
	}

	t, err := template.New(shell).Funcs(fnMap).Parse(tmplStr)
	if err != nil {
		return "", errors.Wrap(err)
	}

	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", errors.Wrap(err)
	}

	return b.String(), nil
}
