package docker

import (
	"context"
	"errors"
	"testing"
)

func TestExecCommand_ReportsExitStatus(t *testing.T) {
	if err := execCommand(context.Background(), []string{"true"}); err != nil {
		t.Errorf("true: unexpected error %v", err)
	}

	if err := execCommand(context.Background(), []string{"sh", "-c", "exit 3"}); err == nil {
		t.Error("sh exit 3: expected error, got nil")
	}
}

func TestExecReplace_MissingBinaryIsError(t *testing.T) {
	if err := execReplace([]string{"dkglaze-no-such-binary"}); err == nil {
		t.Error("expected error for a missing binary, got nil")
	}
}

func TestWithDeps_PassesWiredDepsToCallback(t *testing.T) {
	err := withDeps(func(d *deps) error {
		if d.images == nil || d.runCmd == nil || d.execCmd == nil {
			t.Errorf("deps not fully wired: %+v", d)
		}

		return errFake
	})
	if !errors.Is(err, errFake) {
		t.Errorf("want callback error passed through, got %v", err)
	}
}
