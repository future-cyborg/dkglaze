package errors_test

import (
	goerrors "errors"
	"strings"
	"testing"

	"github.com/future-cyborg/ldocker/internal/errors"
)

var (
	errTest  = goerrors.New("test error")
	errOther = goerrors.New("other error")
)

func TestAs(t *testing.T) {
	cases := map[string]struct {
		err    error
		target interface{}
	}{
		"errTest as errTest?": {err: errTest, target: errTest},
		"wrapped as errTest?": {err: errors.Wrap(errTest), target: errTest},
		"errTest as wrapped?": {err: errTest, target: errors.Wrap(errTest)},
		"wrapped as wrapped?": {err: errors.Wrap(errTest), target: errors.Wrap(errTest)},
	}

	for name, testCase := range cases {
		want := goerrors.As(testCase.err, &testCase.target)
		got := errors.As(testCase.err, &testCase.target)

		if want != got {
			t.Errorf("%v: want %v, got %v", name, want, got)
		}
	}
}

func TestIs(t *testing.T) {
	cases := map[string]struct {
		err    error
		target error
	}{
		"is errTest errTest?": {err: errTest, target: errTest},
		"is wrapped errTest?": {err: errors.Wrap(errTest), target: errTest},
		"is errTest wrapped?": {err: errTest, target: errors.Wrap(errTest)},
		"is wrapped wrapped?": {err: errors.Wrap(errTest), target: errors.Wrap(errTest)},
	}

	for name, testCase := range cases {
		want := goerrors.Is(testCase.err, testCase.target)
		got := errors.Is(testCase.err, testCase.target)

		if want != got {
			t.Errorf("%v: want %v, got %v", name, want, got)
		}
	}
}

func TestWrap(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		if errors.Wrap(nil) != nil {
			t.Error("expected nil")
		}
	})

	t.Run("wraps error", func(t *testing.T) {
		wrapped := errors.Wrap(errTest)
		if wrapped == nil {
			t.Fatal("expected non-nil")
		}
		if !errors.Is(wrapped, errTest) {
			t.Error("wrapped error should be unwrappable to original")
		}
	})

	t.Run("error message contains file and line", func(t *testing.T) {
		wrapped := errors.Wrap(errTest)
		msg := wrapped.Error()
		if !strings.Contains(msg, "errors_test.go") {
			t.Errorf("expected file info in error message, got: %s", msg)
		}
		if !strings.Contains(msg, "test error") {
			t.Errorf("expected original error message, got: %s", msg)
		}
	})

	t.Run("details appended to message", func(t *testing.T) {
		wrapped := errors.Wrap(errTest, "extra detail")
		if !strings.Contains(wrapped.Error(), "extra detail") {
			t.Errorf("expected details in message, got: %s", wrapped.Error())
		}
	})
}

func TestUnwrap(t *testing.T) {
	t.Run("unwrap nil returns nil", func(t *testing.T) {
		if errors.Unwrap(nil) != nil {
			t.Error("expected nil")
		}
	})

	t.Run("unwrap wrapped returns original", func(t *testing.T) {
		wrapped := errors.Wrap(errTest)
		got := errors.Unwrap(wrapped)
		if !errors.Is(got, errTest) {
			t.Errorf("want %v, got %v", errTest, got)
		}
	})

	t.Run("unwrap plain error returns nil", func(t *testing.T) {
		if errors.Unwrap(errTest) != nil {
			t.Error("expected nil")
		}
	})
}

func TestJoin(t *testing.T) {
	t.Run("join nil errors returns nil", func(t *testing.T) {
		if errors.Join(nil, nil) != nil {
			t.Error("expected nil")
		}
	})

	t.Run("join two errors contains both", func(t *testing.T) {
		joined := errors.Join(errTest, errOther)
		if joined == nil {
			t.Fatal("expected non-nil")
		}
		msg := joined.Error()
		if !strings.Contains(msg, errTest.Error()) || !strings.Contains(msg, errOther.Error()) {
			t.Errorf("expected both errors in message, got: %s", msg)
		}
	})
}

func TestOuterError(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		if errors.OuterError(nil) != nil {
			t.Error("expected nil")
		}
	})

	t.Run("single error returns itself", func(t *testing.T) {
		got := errors.OuterError(errTest)
		if !errors.Is(got, errTest) {
			t.Errorf("want %v, got %v", errTest, got)
		}
	})

	t.Run("double wrapped returns first wrap", func(t *testing.T) {
		inner := errors.Wrap(errTest)
		outer := errors.Wrap(inner)
		got := errors.OuterError(outer)
		if !errors.Is(got, inner) {
			t.Errorf("expected first wrap layer, got: %v", got)
		}
	})

	t.Run("wrapped joined error returns joined error", func(t *testing.T) {
		joined := errors.Join(errTest, errOther)
		outer := errors.Wrap(joined)
		got := errors.OuterError(outer)
		if !errors.Is(got, joined) {
			t.Errorf("expected joined error, got: %v", got)
		}
	})
}
