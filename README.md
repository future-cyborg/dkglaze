# udocker

<!-- TODO: describe this repo -->

## Structure

- `cmd/` — application entrypoints (one directory per binary)
- `internal/` — private packages, not importable by external repos
- `pkg/` — public packages, importable by external repos
- `test/` — integration and end-to-end tests

## Make targets

```
make bin                              all bin/ targets
make bin/$(PLATFORM)                  all bin/$(PLATFORM)/ targets
make bin/$(PLATFORM)/$(BUILD)

make docker                           all docker/ targets
make docker/$(PLATFORM)               all docker/$(PLATFORM)/ targets
make docker/$(PLATFORM)/$(BUILD)

make bench-test                       run bench tests
make bench-test/$(GO_PKG)             run bench tests on single package
make unit-test                        run unit tests
make unit-test/$(GO_PKG)              run unit tests on single package
make unit-test-cover                  open coverage heatmap in browser
make unit-test-cover/$(GO_PKG)        open coverage heatmap in browser, single package

make go/generate                      apply go generators
make go/format                        apply lint fixers
make go/lint                          runs linters
make go/tidy                          update go.mod from source code

make print-BUILDS                     print available build targets
make print-PLATFORMS                  print supported platforms

make clean                            removes bin/ report/

make tag/bump-minor-and-push          tag minor version and push
make tag/bump-patch-and-push          tag patch version and push
```

## Private modules

If this repo imports private Go modules, configure `GOPRIVATE` and `.netrc` in the `Dockerfile`:

1. Set `GOPRIVATE` in the `base` stage:

```dockerfile
ENV GOPRIVATE=
```

2. Uncomment the `.netrc` block in the `base` stage and supply credentials:

```dockerfile
ARG VCS_LOGIN
ARG VCS_TOKEN
RUN echo "machine github.com login ${VCS_LOGIN} password ${VCS_TOKEN}" > ~/.netrc
```

3. Add the corresponding `--build-arg` entries to `BUILD_ARGS` in the `Makefile`:

```makefile
BUILD_ARGS         += --build-arg VCS_LOGIN=$(VCS_LOGIN)
BUILD_ARGS         += --build-arg VCS_TOKEN=$(VCS_TOKEN)
```