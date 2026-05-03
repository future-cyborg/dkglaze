# udocker

`udocker` builds and runs personalized Docker images by layering user-specific customizations on top of an existing base image. You define a set of Dockerfile templates and build arguments in a config file, and `udocker` handles chaining the builds, tagging the result, and running the final image.

A typical use case: take a standard image like `ubuntu:22.04` and automatically layer your dotfiles, user account, tools, or SSH keys on top of it — reproducibly.

## Installation

Requires `make` and `docker`.

```sh
make install   # builds the binary and copies it to ~/.local/bin/udocker
```

## Usage

```
udocker <command> [flags]
```

| Command | Description |
|---|---|
| `init` | Create the config directory and default `config.yml` |
| `build <image> [-- build-args...]` | Build the user layer on top of `<image>` |
| `run <image> [-- args...]` | Run the user-layer image (builds it first if it doesn't exist) |
| `clean [-n]` | Remove all images created by udocker (`-n` for dry run) |
| `list` | List all images created by udocker |

**Global flag:** `--verbose` — shows full error stack traces.

### Examples

```sh
udocker init
udocker build ubuntu:22.04
udocker run ubuntu:22.04
udocker clean -n   # preview what would be removed
udocker clean      # remove all udocker-managed images
```

## How it works

### Build

`udocker build <image>` is a wrapper around `docker build`. It does not build anything itself — it generates a sequence of `docker build` invocations driven by your config.

For each template listed under `build.templates`, udocker:

1. Renders the `.dockerfile` template, substituting `{{ .image }}` with the current source image tag.
2. Runs `docker build --label com.udocker=true -t <intermediate-tag> -f <rendered-dockerfile> <context-dir>` with any configured `build.args` appended as `--build-arg` flags.
3. Uses the output image as the source for the next template.

After all templates have run, the final intermediate image is retagged as `<base>-<suffix>:<tag>` and the intermediates are removed. If any step fails, all intermediate images are cleaned up automatically.

For example, with `image = ubuntu:22.04`, `suffix = udocker`, and two templates `base` and `user`, udocker runs:

```
docker build ... -t ubuntu-udocker.0:22.04 -f base.dockerfile  # step 0: FROM ubuntu:22.04
docker build ... -t ubuntu-udocker.1:22.04 -f user.dockerfile  # step 1: FROM ubuntu-udocker.0:22.04
docker tag ubuntu-udocker.1:22.04 ubuntu-udocker:22.04         # final tag
```

Extra args can be passed directly to every `docker build` call by appending them after `--`:

```sh
udocker build ubuntu:22.04 -- --no-cache --build-arg FOO=bar
```

### Run

`udocker run <image>` is a wrapper around `docker run`. It resolves the user-layer image name, builds it if it doesn't exist yet, then runs:

```
docker run <run.args from config> <args from CLI> <image>-<suffix>:<tag>
```

Arguments from `run.args` in the config are always prepended, followed by any args passed on the command line after `--`. This lets you set persistent defaults (e.g. `-it --rm -v $HOME:/home/user`) in config while still being able to pass one-off flags at runtime:

```sh
udocker run ubuntu:22.04 -- -e DEBUG=1
# executes: docker run -it --rm -e DEBUG=1 ubuntu-udocker:22.04
```

## Configuration

Config lives at `~/.config/udocker/config.yml`. Run `udocker init` to create it with defaults.

```yaml
defaults:
  suffix: udocker   # (required) Appended to the base image name to form the output image name.
                    # e.g. ubuntu:22.04 → ubuntu-udocker:22.04

build:
  templates:        # Ordered list of Dockerfile template names to apply in sequence.
    - base          # Resolves to ~/.config/udocker/templates/base.dockerfile
    - user          # Resolves to ~/.config/udocker/templates/user.dockerfile

  args:             # Build arguments passed to every docker build step.
    - key: USERNAME
      value: alice              # Static: value is used as-is.
    - key: MY_TOKEN
      cmd: pass show my-token   # Dynamic: shell command is run at config-load time;
                                # trimmed stdout becomes the value.

run:
  args:             # Arguments always prepended to docker run.
    - -it
    - --rm
    - -v
    - /home/alice:/home/alice
```

The config file is validated against a JSON Schema on load. The only required field is `defaults.suffix`. No unknown top-level keys are permitted.

### Dockerfile templates

Templates live in `~/.config/udocker/templates/` and use Go's [`text/template`](https://pkg.go.dev/text/template) syntax. The only variable provided is `{{ .image }}`, which holds the source image tag for that build step (either the original base image or the output of the previous step).

```dockerfile
# ~/.config/udocker/templates/base.dockerfile
FROM {{ .image }}
RUN apt-get update && apt-get install -y vim git

# ~/.config/udocker/templates/user.dockerfile
FROM {{ .image }}
ARG USERNAME
RUN useradd -m $USERNAME
```

**Build context:** if a directory `~/.config/udocker/templates/<name>/` exists, it is used as the Docker build context for that step (useful for `COPY` instructions). Otherwise the `templates/` directory itself is used.

Every image built by `udocker` is labeled `com.udocker=true`, which is how `udocker clean` finds and removes them.

## Development

All build, lint, and test targets run inside Docker via BuildKit. Only `make` and `docker` are required on the host.

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
