// Package errors provides error handling utilities.
package errors

import (
	goerrors "errors"
	"fmt"
	"runtime"
	"strings"
)

// WrappedError wraps an error.
type WrappedError struct {
	err     error
	file    string
	line    int
	details []any
}

// As returns true if the given error is of the same type as the target.
func As(err error, target any) bool {
	return goerrors.As(err, target)
}

// Is returns true if the given error is the same as the target.
func Is(err, target error) bool {
	return goerrors.Is(err, target)
}

// Join joins the given errors.
func Join(errs ...error) error {
	return goerrors.Join(errs...)
}

// Unwrap returns the unwrapped error.
func Unwrap(err error) error {
	return goerrors.Unwrap(err)
}

// OuterError returns the outermost meaningful error — the direct wrapper around
// the root cause. For a chain A→B→C→nil it returns B, giving a single clean
// message without full stack-trace context.
//
// When the chain terminates at a multi-error (e.g. errors.Join), that joined
// error is itself returned, since it has no single unwrappable root.
func OuterError(err error) error {
	type multiUnwrap interface{ Unwrap() []error }

	wErr := err
	for {
		unwrap := Unwrap(err)
		if unwrap == nil {
			if _, ok := err.(multiUnwrap); ok {
				return err
			}

			return wErr
		}
		wErr = err
		err = unwrap
	}
}

// Wrap returns the given error wrapped with file:line information.
func Wrap(err error, details ...any) error {
	if err == nil {
		return nil
	}

	_, file, line, _ := runtime.Caller(1)

	return &WrappedError{err: err, file: file, line: line, details: details}
}

// Error returns a string representation of the error.
func (w *WrappedError) Error() string {
	s := []string{fmt.Sprintf("%s:%d: %s", w.file, w.line, w.err.Error())}

	for i := range w.details {
		s = append(s, ": ", fmt.Sprint(w.details[i]))
	}

	return strings.Join(s, "")
}

// Unwrap returns the wrapped error.
func (w *WrappedError) Unwrap() error {
	return w.err
}
