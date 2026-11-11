// Package logger is for logging and outputting messages to the console.
package logger

import (
	"fmt"
	"os"
)

// Color represents a color.
type Color int

//nolint:revive // exported Color constants without prefix are intentional for readability
const (
	Reset Color = iota
	Red
	Green
	Yellow
	Blue
	Magenta
	Cyan
	White
)

func colorCodes(color Color) string {
	codes := [...]string{
		"\x1b[0m",
		"\x1b[31m",
		"\x1b[32m",
		"\x1b[33m",
		"\x1b[34m",
		"\x1b[35m",
		"\x1b[36m",
		"\x1b[37m",
	}

	if int(color) < 0 || int(color) >= len(codes) {
		return ""
	}

	return codes[color]
}

func isTTY(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}

	return stat.Mode()&os.ModeCharDevice != 0
}

// Colorize returns text with color codes.
func Colorize(text string, color Color) string {
	return fmt.Sprintf("%s%s%s", colorCodes(color), text, colorCodes(Reset))
}

func colorizeIfTTY(text string, color Color, f *os.File) string {
	if !isTTY(f) {
		return text
	}

	return Colorize(text, color)
}

// PrintColor prints text in color to stdout.
func PrintColor(color Color, text string) {
	//nolint:forbidigo // logger is the sanctioned print sink
	fmt.Println(colorizeIfTTY(text, color, os.Stdout))
}

// PrintColorF prints text in color with formatting to stdout.
func PrintColorF(color Color, pattern string, text ...any) {
	//nolint:forbidigo // logger is the sanctioned print sink
	fmt.Println(colorizeIfTTY(fmt.Sprintf(pattern, text...), color, os.Stdout))
}

// PrintColorErr prints text in color to stderr.
func PrintColorErr(color Color, text string) {
	fmt.Fprintln(os.Stderr, colorizeIfTTY(text, color, os.Stderr))
}

// PrintColorFErr prints text in color with formatting to stderr.
func PrintColorFErr(color Color, pattern string, text ...any) {
	fmt.Fprintln(os.Stderr, colorizeIfTTY(fmt.Sprintf(pattern, text...), color, os.Stderr))
}
