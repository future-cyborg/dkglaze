package docker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

var errInspectFake = errors.New("fake inspect failure")

// setEnv sets key to value for the duration of the test. An empty value
// unsets the variable (the docker CLI treats empty as unset too).
func setEnv(t *testing.T, key, value string) {
	t.Helper()

	t.Setenv(key, value)

	if value == "" {
		_ = os.Unsetenv(key)
	}
}

// stubInspect replaces inspectContext for the duration of the test. It returns
// a pointer to the number of times the stub was invoked.
func stubInspect(t *testing.T, out string, err error) *int {
	t.Helper()

	calls := new(int)
	orig := inspectContext

	inspectContext = func(context.Context) ([]byte, error) {
		*calls++

		return []byte(out), err
	}

	t.Cleanup(func() { inspectContext = orig })

	return calls
}

// isFromEnv reports whether opt is the client.FromEnv option.
func isFromEnv(opt client.Opt) bool {
	return reflect.ValueOf(opt).Pointer() == reflect.ValueOf(client.FromEnv).Pointer()
}

// ctxJSON renders a one-element `docker context inspect` array. tlsFiles, when
// non-empty, populates TLSMaterial["docker"].
func ctxJSON(name, host string, skipTLS bool, tlsPath string, tlsFiles ...string) string {
	tlsMaterial := map[string]any{}
	if len(tlsFiles) > 0 {
		tlsMaterial["docker"] = tlsFiles
	}

	obj := map[string]any{
		"Name":        name,
		"Endpoints":   map[string]any{"docker": map[string]any{"Host": host, "SkipTLSVerify": skipTLS}},
		"TLSMaterial": tlsMaterial,
		"Storage":     map[string]any{"TLSPath": tlsPath},
	}

	out, err := json.Marshal([]any{obj})
	if err != nil {
		panic(err)
	}

	return string(out)
}

func TestClientOpts(t *testing.T) {
	tlsFilesFixture := []string{"ca.pem", "cert.pem", "key.pem"}

	defaultContextJSON := ctxJSON(defaultContextName, "unix:///var/run/docker.sock", false, "")
	colimaContextJSON := ctxJSON("colima", "unix:///Users/u/.colima/default/docker.sock", false,
		"/Users/u/.docker/contexts/meta/x")
	tcpContextJSON := ctxJSON("remote", "tcp://10.0.0.5:2375", false, "")
	tlsContextJSON := ctxJSON("secure", "tcp://ci.example:2376", false,
		"/home/u/.docker/contexts/tls/abc", tlsFilesFixture...)
	sshContextJSON := ctxJSON("remote-ssh", "ssh://user@build.example", false, "")
	skipTLSTCPContextJSON := ctxJSON("insecure", "tcp://10.0.0.5:2376", true, "")
	skipTLSUnixJSON := ctxJSON("skipunix", "unix:///tmp/d.sock", true, "")
	emptyHostContextJSON := ctxJSON("nohost", "", false, "")

	// Real CLI output carries extra fields (Metadata, Description, other endpoints).
	extraFieldsContextJSON := `[{"Name":"extra","Description":"x",` +
		`"Metadata":{"StackOrchestrator":"swarm"},` +
		`"Endpoints":{"docker":{"Host":"unix:///tmp/e.sock","SkipTLSVerify":false},` +
		`"kubernetes":{"Host":""}},` +
		`"TLSMaterial":{},"Storage":{"MetadataPath":"<IN MEMORY>","TLSPath":"<IN MEMORY>"}}]`

	twoContextsJSON := `[{"Name":"a"},{"Name":"b"}]`

	cases := map[string]struct {
		dockerHost  string
		dockerCtx   string
		inspectOut  string
		inspectErr  error
		wantCalls   int
		wantOpts    int
		wantFromEnv bool
		wantErrs    []error
	}{
		"DOCKER_HOST only skips inspect": {
			dockerHost:  "tcp://10.0.0.5:2375",
			inspectErr:  errInspectFake, // must not be consulted
			wantCalls:   0,
			wantOpts:    1,
			wantFromEnv: true,
		},
		"DOCKER_HOST and DOCKER_CONTEXT both set consults inspect": {
			dockerHost: "tcp://10.0.0.5:2375",
			dockerCtx:  "colima",
			inspectOut: colimaContextJSON,
			wantCalls:  1,
			wantOpts:   2,
		},
		"default context falls back to env": {
			inspectOut:  defaultContextJSON,
			wantCalls:   1,
			wantOpts:    1,
			wantFromEnv: true,
		},
		"unix socket context": {
			inspectOut: colimaContextJSON,
			wantCalls:  1,
			wantOpts:   2,
		},
		"plain tcp context": {
			inspectOut: tcpContextJSON,
			wantCalls:  1,
			wantOpts:   2,
		},
		"TLS context adds client TLS config": {
			inspectOut: tlsContextJSON,
			wantCalls:  1,
			wantOpts:   3,
		},
		"ssh context is an explicit error": {
			inspectOut: sshContextJSON,
			wantCalls:  1,
			wantErrs:   []error{errSSHContext},
		},
		"skip-tls-verify on tcp is an explicit error": {
			inspectOut: skipTLSTCPContextJSON,
			wantCalls:  1,
			wantErrs:   []error{errSkipTLSVerifyContext},
		},
		"skip-tls-verify on unix socket is ignored": {
			inspectOut: skipTLSUnixJSON,
			wantCalls:  1,
			wantOpts:   2,
		},
		"unknown JSON fields are ignored": {
			inspectOut: extraFieldsContextJSON,
			wantCalls:  1,
			wantOpts:   2,
		},
		"inspect failure is not silently swallowed": {
			inspectErr: errInspectFake,
			wantCalls:  1,
			wantErrs:   []error{errContextInspect, errInspectFake},
		},
		"malformed JSON is an explicit error": {
			inspectOut: `[{"Name":`,
			wantCalls:  1,
			wantErrs:   []error{errUnexpectedContextOutput},
		},
		"empty array is an explicit error": {
			inspectOut: `[]`,
			wantCalls:  1,
			wantErrs:   []error{errUnexpectedContextOutput},
		},
		"multiple contexts is an explicit error": {
			inspectOut: twoContextsJSON,
			wantCalls:  1,
			wantErrs:   []error{errUnexpectedContextOutput},
		},
		"empty host is an explicit error": {
			inspectOut: emptyHostContextJSON,
			wantCalls:  1,
			wantErrs:   []error{errUnexpectedContextOutput},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setEnv(t, "DOCKER_HOST", tc.dockerHost)
			setEnv(t, "DOCKER_CONTEXT", tc.dockerCtx)

			calls := stubInspect(t, tc.inspectOut, tc.inspectErr)

			opts, err := clientOpts(context.Background())

			if *calls != tc.wantCalls {
				t.Errorf("inspect calls: want %d, got %d", tc.wantCalls, *calls)
			}

			if len(tc.wantErrs) > 0 {
				for _, want := range tc.wantErrs {
					if !errors.Is(err, want) {
						t.Errorf("want error matching %q, got %v", want, err)
					}
				}

				if opts != nil {
					t.Errorf("want nil opts on error, got %d", len(opts))
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(opts) != tc.wantOpts {
				t.Fatalf("opts: want %d, got %d", tc.wantOpts, len(opts))
			}

			if got := isFromEnv(opts[0]); got != tc.wantFromEnv {
				t.Errorf("FromEnv: want %v, got %v", tc.wantFromEnv, got)
			}
		})
	}
}

func TestClientOpts_ErrorMessagesAreClear(t *testing.T) {
	setEnv(t, "DOCKER_HOST", "")
	setEnv(t, "DOCKER_CONTEXT", "")

	stubInspect(t, ctxJSON("remote-ssh", "ssh://user@build.example", false, ""), nil)

	_, err := clientOpts(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ssh://") {
		t.Errorf("ssh error should name the ssh:// limitation, got %v", err)
	}
}

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
	setEnv(t, "DOCKER_HOST", "")
	setEnv(t, "DOCKER_CONTEXT", "")
	stubInspect(t, ctxJSON(defaultContextName, "unix:///var/run/docker.sock", false, ""), nil)

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
