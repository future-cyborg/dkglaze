# Contributing

This covers building dkglaze from source and contributing to it. If you just want to run dkglaze, see the [README](../README.md) instead.

## Design philosophy

dkglaze is intentionally unopinionated. The tool itself is just a generic mechanism for layering build/run customization (templates, args, mounts) on top of a base image. It's deliberately not a place to absorb every Docker workflow script that's convenient for one person — things like registry push/pull helpers, compose-style multi-container orchestration, or other automation around Docker. Adding those to the tool itself would tailor it to one particular way of working with Docker instead of staying a general-purpose layering mechanism. That kind of workflow-specific tooling belongs in scripts around dkglaze, not in it. Keep this in mind for contributions: prefer extending what dkglaze lets you express over growing what it does.

## Prerequisites

- `make`
- Docker with BuildKit. The Makefile sets `DOCKER_BUILDKIT=1` and runs every build, lint, and test step through `docker build` against the Dockerfile. No Go toolchain is needed on the host for these targets — the exception is `unit-test-cover`, which runs `go tool cover` on the host and so needs a Go toolchain.
- **Linux.** The Makefile's build/lint/test targets are only configured and tested on Linux. They are not tested on macOS and may not work there, even though dkglaze itself (the released binary) targets both linux and darwin. A PR extending the Make/Docker-based dev tooling to work on macOS would be welcome.

## Submitting changes

Open a PR against `main`. CI runs lint, tests, and a `go mod tidy` check — run `make go/lint` and `make unit-test` locally first.

## Build and install from source

```sh
make install   # runs `make build`, then copies bin/<os>/<arch>/dkglaze to ~/.local/bin/dkglaze
```

`~/.local/bin` must already exist and be on your `PATH`.

## Make targets

```
make bin                              all bin/ targets
make bin/$(PLATFORM)                  all bin/$(PLATFORM)/ targets
make bin/$(PLATFORM)/$(BUILD)

make bench-test                       run bench tests
make bench-test/$(GO_PKG)             run bench tests on single package
make unit-test                        run unit tests
make unit-test/$(GO_PKG)              run unit tests on single package
make unit-test-cover                  open coverage heatmap in browser
make unit-test-cover/$(GO_PKG)        open coverage heatmap in browser, single package

make go/format                        apply lint fixers
make go/lint                          runs linters
make go/tidy                          update go.mod from source code

make print-BUILDS                     print available build targets
make print-PLATFORMS                  print supported platforms

make clean                            removes bin/ report/

make tag/bump-minor-and-push          tag minor version and push
make tag/bump-patch-and-push          tag patch version and push
```
