package logger_test

import (
	"strings"
	"testing"

	"github.com/future-cyborg/dkglaze/internal/logger"
)

func TestColorize(t *testing.T) {
	cases := []struct {
		name  string
		color logger.Color
		text  string
	}{
		{"red", logger.Red, "error message"},
		{"green", logger.Green, "success"},
		{"yellow", logger.Yellow, "warning"},
		{"cyan", logger.Cyan, "info"},
		{"reset", logger.Reset, "plain"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := logger.Colorize(tc.text, tc.color)

			if !strings.Contains(result, tc.text) {
				t.Errorf("expected result to contain %q, got %q", tc.text, result)
			}

			// Must start with an ANSI escape and end with reset code
			if !strings.HasPrefix(result, "\x1b[") {
				t.Errorf("expected ANSI prefix, got %q", result)
			}

			if !strings.HasSuffix(result, "\x1b[0m") {
				t.Errorf("expected reset suffix, got %q", result)
			}
		})
	}
}

func TestColorize_DifferentColors(t *testing.T) {
	red := logger.Colorize("text", logger.Red)
	green := logger.Colorize("text", logger.Green)

	if red == green {
		t.Error("expected different ANSI codes for different colors")
	}
}

func TestColorize_EmptyString(t *testing.T) {
	result := logger.Colorize("", logger.Cyan)

	if result == "" {
		t.Error("expected non-empty result even for empty input (ANSI codes)")
	}
}
