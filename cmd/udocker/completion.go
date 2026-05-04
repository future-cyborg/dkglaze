package main

import (
	"fmt"
	"strings"

	"github.com/alecthomas/kong"
)

const posComplImages = "images"

type cmdMeta struct {
	name     string
	flags    []flagMeta
	posCompl string // "images", "enum:bash,zsh", or ""
	children []cmdMeta
}

type flagMeta struct {
	long   string
	short  rune
	isBool bool
}

// --- model collection ---

func flagMetaFromNode(node *kong.Node) []flagMeta {
	var out []flagMeta

	for _, f := range node.Flags {
		if !f.Hidden {
			out = append(out, flagMeta{long: f.Name, short: f.Short, isBool: f.IsBool()})
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
			name:     child.Name,
			flags:    flagMetaFromNode(child),
			posCompl: posComplFromNode(child),
		}

		if len(child.Children) > 0 {
			m.children = collectCmdMeta(child)
		}

		out = append(out, m)
	}

	return out
}

// --- entry point ---

func generateCompletion(shell string, app *kong.Application) string {
	cmds := collectCmdMeta(app.Node)

	switch shell {
	case "bash":
		return generateBashScript(app.Name, cmds)
	case "zsh":
		return generateZshScript(app.Name, cmds)
	}

	return ""
}

// --- bash ---

func generateBashScript(appName string, cmds []cmdMeta) string {
	var b strings.Builder

	fnName := "_" + strings.ReplaceAll(appName, "-", "_") + "_completions"

	writeBashHeader(&b, fnName, cmds)
	writeBashFlagSection(&b, cmds)
	writeBashValueFlagGuard(&b, cmds)
	writeBashPosSection(&b, cmds)

	fmt.Fprintf(&b, "    esac\n}\n")
	fmt.Fprintf(&b, "complete -F %s %s\n", fnName, appName)

	return b.String()
}

func writeBashHeader(b *strings.Builder, fnName string, cmds []cmdMeta) {
	var names []string

	for _, c := range cmds {
		names = append(names, c.name)
	}

	fmt.Fprintf(b, "%s() {\n", fnName)
	fmt.Fprintf(b, "    local cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	fmt.Fprintf(b, "    local prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	fmt.Fprintf(b, "    local cmd=\"${COMP_WORDS[1]}\"\n\n")
	fmt.Fprintf(b, "    if [[ \"${COMP_CWORD}\" -eq 1 ]]; then\n")
	fmt.Fprintf(b, "        COMPREPLY=($(compgen -W %q -- \"${cur}\"))\n", strings.Join(names, " "))
	fmt.Fprintf(b, "        return\n    fi\n\n")
}

func writeBashFlagSection(b *strings.Builder, cmds []cmdMeta) {
	fmt.Fprintf(b, "    if [[ \"${cur}\" == -* ]]; then\n")
	fmt.Fprintf(b, "        case \"${cmd}\" in\n")

	for _, c := range cmds {
		writeBashFlagCase(b, &c)
	}

	fmt.Fprintf(b, "        esac\n")
	fmt.Fprintf(b, "        return\n    fi\n\n")
}

func writeBashFlagCase(b *strings.Builder, c *cmdMeta) {
	if len(c.children) == 0 {
		if len(c.flags) > 0 {
			fmt.Fprintf(b, "            %s) COMPREPLY=($(compgen -W %q -- \"${cur}\"));;\n",
				c.name, flagWords(c.flags))
		}

		return
	}

	if !anyHasFlags(c.children) {
		return
	}

	fmt.Fprintf(b, "            %s)\n", c.name)
	fmt.Fprintf(b, "                local subcmd=\"${COMP_WORDS[2]}\"\n")
	fmt.Fprintf(b, "                case \"${subcmd}\" in\n")

	for _, child := range c.children {
		if len(child.flags) > 0 {
			fmt.Fprintf(b, "                    %s) COMPREPLY=($(compgen -W %q -- \"${cur}\"));;\n",
				child.name, flagWords(child.flags))
		}
	}

	fmt.Fprintf(b, "                esac\n")
	fmt.Fprintf(b, "                ;;\n")
}

func anyHasFlags(children []cmdMeta) bool {
	for _, child := range children {
		if len(child.flags) > 0 {
			return true
		}
	}

	return false
}

func addValueFlagPattern(f flagMeta, seen map[string]bool, pats *[]string) {
	if f.isBool {
		return
	}

	long := "--" + f.long
	if !seen[long] {
		*pats = append(*pats, long)
		seen[long] = true
	}

	if f.short == 0 {
		return
	}

	short := "-" + string(f.short)
	if !seen[short] {
		*pats = append(*pats, short)
		seen[short] = true
	}
}

func collectValueFlagPatterns(cmds []cmdMeta) []string {
	var pats []string

	seen := map[string]bool{}

	for _, c := range cmds {
		for _, f := range c.flags {
			addValueFlagPattern(f, seen, &pats)
		}
	}

	return pats
}

func writeBashValueFlagGuard(b *strings.Builder, cmds []cmdMeta) {
	pats := collectValueFlagPatterns(cmds)
	if len(pats) == 0 {
		return
	}

	fmt.Fprintf(b, "    case \"${prev}\" in\n")
	fmt.Fprintf(b, "        %s) return;;\n", strings.Join(pats, "|"))
	fmt.Fprintf(b, "    esac\n\n")
}

func writeBashPosSection(b *strings.Builder, cmds []cmdMeta) {
	fmt.Fprintf(b, "    case \"${cmd}\" in\n")

	for _, c := range cmds {
		writeBashPosCase(b, &c)
	}
}

func writeBashPosCase(b *strings.Builder, c *cmdMeta) {
	switch {
	case c.posCompl == posComplImages:
		writeBashImagesCase(b, c.name, "            ")
	case strings.HasPrefix(c.posCompl, "enum:"):
		writeBashEnumCase(b, c.name, c.posCompl)
	case len(c.children) > 0:
		writeBashChildrenCase(b, c)
	}
}

func writeBashImagesCase(b *strings.Builder, cmdName, indent string) {
	fmt.Fprintf(b, "        %s)\n", cmdName)
	fmt.Fprintf(b,
		"%sCOMPREPLY=($(compgen -W \"$(\"${COMP_WORDS[0]}\" _list-images 2>/dev/null)\" -- \"${cur}\"))\n",
		indent)
	fmt.Fprintf(b, "            ;;\n")
}

func writeBashEnumCase(b *strings.Builder, cmdName, posCompl string) {
	vals := strings.ReplaceAll(strings.TrimPrefix(posCompl, "enum:"), ",", " ")
	fmt.Fprintf(b, "        %s) COMPREPLY=($(compgen -W %q -- \"${cur}\"));;\n", cmdName, vals)
}

func writeBashChildrenCase(b *strings.Builder, c *cmdMeta) {
	var childNames []string

	for _, child := range c.children {
		childNames = append(childNames, child.name)
	}

	fmt.Fprintf(b, "        %s)\n", c.name)
	fmt.Fprintf(b, "            local subcmd=\"${COMP_WORDS[2]}\"\n")
	fmt.Fprintf(b, "            if [[ \"${COMP_CWORD}\" -eq 2 ]]; then\n")
	fmt.Fprintf(b, "                COMPREPLY=($(compgen -W %q -- \"${cur}\"))\n",
		strings.Join(childNames, " "))
	fmt.Fprintf(b, "            else\n")
	fmt.Fprintf(b, "                case \"${subcmd}\" in\n")

	for _, child := range c.children {
		writeBashSubCmdCase(b, &child)
	}

	fmt.Fprintf(b, "                esac\n")
	fmt.Fprintf(b, "            fi\n")
	fmt.Fprintf(b, "            ;;\n")
}

func writeBashSubCmdCase(b *strings.Builder, child *cmdMeta) {
	switch {
	case len(child.children) > 0:
		var gcNames []string

		for _, gc := range child.children {
			gcNames = append(gcNames, gc.name)
		}

		fmt.Fprintf(b, "                    %s) COMPREPLY=($(compgen -W %q -- \"${cur}\"));;\n",
			child.name, strings.Join(gcNames, " "))

	case child.posCompl == posComplImages:
		writeBashImagesCase(b, child.name, "                        ")

	case strings.HasPrefix(child.posCompl, "enum:"):
		vals := strings.ReplaceAll(strings.TrimPrefix(child.posCompl, "enum:"), ",", " ")
		fmt.Fprintf(b, "                    %s) COMPREPLY=($(compgen -W %q -- \"${cur}\"));;\n",
			child.name, vals)
	}
}

func flagWords(flags []flagMeta) string {
	var words []string

	for _, f := range flags {
		words = append(words, "--"+f.long)

		if f.short != 0 {
			words = append(words, "-"+string(f.short))
		}
	}

	return strings.Join(words, " ")
}

// --- zsh ---

func generateZshScript(appName string, cmds []cmdMeta) string {
	var b strings.Builder

	writeZshHeader(&b, appName, cmds)
	writeZshCmdCases(&b, cmds)
	writeZshFooter(&b, appName)

	return b.String()
}

func writeZshHeader(b *strings.Builder, appName string, cmds []cmdMeta) {
	var names []string

	for _, c := range cmds {
		names = append(names, c.name)
	}

	fmt.Fprintf(b, "#compdef %s\n\n", appName)
	fmt.Fprintf(b, "_%s() {\n", appName)
	fmt.Fprintf(b, "    local state\n\n")
	fmt.Fprintf(b, "    _arguments \\\n")
	fmt.Fprintf(b, "        '1: :->subcmd' \\\n")
	fmt.Fprintf(b, "        '*: :->args'\n\n")
	fmt.Fprintf(b, "    case $state in\n")
	fmt.Fprintf(b, "        subcmd)\n")
	fmt.Fprintf(b, "            local -a subcmds\n")
	fmt.Fprintf(b, "            subcmds=(%s)\n", strings.Join(names, " "))
	fmt.Fprintf(b, "            _describe 'command' subcmds\n")
	fmt.Fprintf(b, "            ;;\n")
	fmt.Fprintf(b, "        args)\n")
	fmt.Fprintf(b, "            case ${words[2]} in\n")
}

func writeZshCmdCases(b *strings.Builder, cmds []cmdMeta) {
	for _, c := range cmds {
		writeZshCmdCase(b, &c)
	}
}

func writeZshCmdCase(b *strings.Builder, c *cmdMeta) {
	switch {
	case c.posCompl == posComplImages:
		fmt.Fprintf(b, "                %s)\n", c.name)
		fmt.Fprintf(b, "                    local -a images\n")
		fmt.Fprintf(b, "                    images=(${(f)\"$(${words[1]} _list-images 2>/dev/null)\"})\n")
		fmt.Fprintf(b, "                    _describe 'image' images\n")
		fmt.Fprintf(b, "                    ;;\n")

	case strings.HasPrefix(c.posCompl, "enum:"):
		vals := strings.ReplaceAll(strings.TrimPrefix(c.posCompl, "enum:"), ",", " ")
		fmt.Fprintf(b, "                %s)\n", c.name)
		fmt.Fprintf(b, "                    local -a opts\n")
		fmt.Fprintf(b, "                    opts=(%s)\n", vals)
		fmt.Fprintf(b, "                    _describe 'option' opts\n")
		fmt.Fprintf(b, "                    ;;\n")

	case len(c.children) > 0:
		writeZshChildrenCase(b, c)
	}
}

func writeZshChildrenCase(b *strings.Builder, c *cmdMeta) {
	var childNames []string

	for _, child := range c.children {
		childNames = append(childNames, child.name)
	}

	fmt.Fprintf(b, "                %s)\n", c.name)
	fmt.Fprintf(b, "                    if [[ ${#words} -eq 3 ]]; then\n")
	fmt.Fprintf(b, "                        local -a subcmds2\n")
	fmt.Fprintf(b, "                        subcmds2=(%s)\n", strings.Join(childNames, " "))
	fmt.Fprintf(b, "                        _describe 'subcommand' subcmds2\n")
	fmt.Fprintf(b, "                    else\n")
	fmt.Fprintf(b, "                        case ${words[3]} in\n")

	for _, child := range c.children {
		writeZshSubCmdCase(b, &child)
	}

	fmt.Fprintf(b, "                        esac\n")
	fmt.Fprintf(b, "                    fi\n")
	fmt.Fprintf(b, "                    ;;\n")
}

func writeZshSubCmdCase(b *strings.Builder, child *cmdMeta) {
	switch {
	case len(child.children) > 0:
		var gcNames []string

		for _, gc := range child.children {
			gcNames = append(gcNames, gc.name)
		}

		fmt.Fprintf(b, "                            %s)\n", child.name)
		fmt.Fprintf(b, "                                local -a subcmds3\n")
		fmt.Fprintf(b, "                                subcmds3=(%s)\n", strings.Join(gcNames, " "))
		fmt.Fprintf(b, "                                _describe 'subcommand' subcmds3\n")
		fmt.Fprintf(b, "                                ;;\n")

	case child.posCompl == posComplImages:
		fmt.Fprintf(b, "                            %s)\n", child.name)
		fmt.Fprintf(b, "                                local -a images\n")
		fmt.Fprintf(b,
			"                                images=(${(f)\"$(${words[1]} _list-images 2>/dev/null)\"})\n")
		fmt.Fprintf(b, "                                _describe 'image' images\n")
		fmt.Fprintf(b, "                                ;;\n")

	case strings.HasPrefix(child.posCompl, "enum:"):
		vals := strings.ReplaceAll(strings.TrimPrefix(child.posCompl, "enum:"), ",", " ")
		fmt.Fprintf(b, "                            %s)\n", child.name)
		fmt.Fprintf(b, "                                local -a opts\n")
		fmt.Fprintf(b, "                                opts=(%s)\n", vals)
		fmt.Fprintf(b, "                                _describe 'option' opts\n")
		fmt.Fprintf(b, "                                ;;\n")
	}
}

func writeZshFooter(b *strings.Builder, appName string) {
	fmt.Fprintf(b, "            esac\n")
	fmt.Fprintf(b, "            ;;\n")
	fmt.Fprintf(b, "    esac\n}\n\n")
	fmt.Fprintf(b, "_%s \"$@\"\n", appName)
}
