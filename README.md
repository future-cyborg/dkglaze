# udocker

`udocker` builds and runs personalized Docker images by layering user-specific customizations on top of an existing base image. You define **features** (named bundles of Dockerfile templates, build arguments, and run configuration) and **profiles** (ordered combinations of features) in a config file, and `udocker` handles chaining the builds, tagging the result, and running the final image.

A typical use case: take a standard image like `ubuntu:22.04` and automatically layer your dotfiles, user account, tools, or SSH keys on top of it — reproducibly. Switch between a minimal `default` profile and a `gpu-dev` profile with a single flag.

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
| `build <image> [-- build-args...]` | Build the user layer on top of `<image>` (`--pull` to pull the latest base image first) |
| `run <image> [-- args...]` | Run the user-layer image (builds it first if it doesn't exist) |
| `clean [-n]` | Remove all images created by udocker (`-n` for dry run) |
| `list` | List all images created by udocker |
| `config` | Manage configuration (see below) |
| `completion <bash\|zsh>` | Print shell completion script to stdout |

### `udocker config` subcommands

| Subcommand | Description |
|---|---|
| `config init` | Create the config directory and default `config.yml` |
| `config list profiles` | List all profiles and their composed features |
| `config list features` | List all features with a count of what each contributes |
| `config set default <profile>` | Change `defaults.profile` in the config file |
| `config validate` | Validate the config file (schema + cross-references) |
| `config show` | Print the raw config YAML, or `--resolved` to show computed values per profile |
| `config edit` | Open the config file in `$EDITOR` |
| `config path` | Print the config file path |

**Global flag:** `--verbose` — shows full error stack traces with file and line numbers. Without `--verbose`, only the outermost error message is shown.

```
# without --verbose
udocker: unknown profile: "typo"

# with --verbose
udocker: /src/internal/config/config.go:226: /src/internal/config/config.go:115: unknown profile: typo
```

**Profile flag:** `--profile <name>` (or `-p <name>`) on `build` and `run` — overrides `defaults.profile`.

### Examples

```sh
udocker config init
udocker build ubuntu:22.04
udocker run ubuntu:22.04

# Use a non-default profile
udocker build --profile gpu-dev ubuntu:22.04
udocker run -p gpu-dev ubuntu:22.04

udocker clean -n   # preview what would be removed
udocker clean      # remove all udocker-managed images

# Pull the latest base image before rebuilding
udocker build --pull ubuntu:22.04
```

## Shell Completion

Generate and install a completion script for your shell:

```sh
# Bash — filename must match the command name
udocker completion bash > ~/.local/share/bash-completion/completions/udocker

# Zsh — filename must be prefixed with underscore and on $fpath
udocker completion zsh > ~/.zsh/completions/_udocker
```

The zsh file **must** be named `_udocker` (leading underscore) to be picked up by `compinit`.

Completions are generated dynamically from the CLI model. Local Docker images are listed at
tab-press time by calling `udocker _list-images` (images managed by udocker are excluded).

## How it works

### Build

`udocker build <image>` resolves the active profile, merges its features in order, then runs a sequence of `docker build` invocations.

For each template in the merged template list, udocker:

1. Renders the `.dockerfile` template, substituting `{{ .image }}` with the current source image tag.
2. Runs `docker build --label com.udocker=true -t <intermediate-tag> -f <rendered-dockerfile> <context-dir>` with any merged `build.args` appended as `--build-arg` flags.
3. Uses the output image as the source for the next template.

After all templates have run, the final intermediate image is retagged as `<base>-<suffix>:<tag>` and the intermediates are removed. If any step fails, all intermediate images are cleaned up automatically.

For example, with `image = ubuntu:22.04`, `suffix = udocker`, and two templates `base` and `user`, udocker runs:

```
docker build ... -t ubuntu-udocker-default.0:22.04 -f base.dockerfile  # step 0: FROM ubuntu:22.04
docker build ... -t ubuntu-udocker-default.1:22.04 -f user.dockerfile  # step 1: FROM ubuntu-udocker-default.0:22.04
docker tag ubuntu-udocker-default.1:22.04 ubuntu-udocker-default:22.04 # final tag
```

Extra args can be passed directly to every `docker build` call by appending them after `--`:

```sh
udocker build ubuntu:22.04 -- --no-cache --build-arg FOO=bar
```

### Run

`udocker run <image>` resolves the active profile, merges its features, then runs:

```
docker run [merged run.args] [--volume <mount>...] [CLI args] <image>-<suffix>:<tag>
```

Mounts from `run.mounts` are expanded (`~` and `$VAR`) and passed as `--volume` flags before any CLI args. This lets you set persistent volume mounts in config while still passing one-off flags at runtime:

```sh
udocker run ubuntu:22.04 -- -e DEBUG=1
# executes: docker run -it --rm --volume /home/alice/.config/opencode:/root/.config/opencode -e DEBUG=1 ubuntu-udocker:22.04
```

## Configuration

Config lives at `~/.config/udocker/config.yml`. Run `udocker config init` to create it with defaults.

To use a different config directory (useful for testing or multiple configurations), set the `UDOCKER_CONFIG_DIR` environment variable:

```sh
UDOCKER_CONFIG_DIR=~/my-other-config udocker build ubuntu:22.04
```

All configuration is organized into **features** and **profiles**. There are no top-level `build` or `run` sections.

```yaml
defaults:
  suffix: udocker     # (required) Appended to base image name along with the active profile name.
                      # e.g. ubuntu:22.04 + suffix "udocker" + profile "default" → ubuntu-udocker-default:22.04
  profile: default    # (required) Profile used when --profile flag is not set

features:
  base:
    build:
      templates:      # Ordered list of Dockerfile template names applied in sequence.
        - base        # Resolves to ~/.config/udocker/templates/base.dockerfile
      args:
        - key: USERNAME
          value: alice              # Static value.
        - key: MY_TOKEN
          cmd: pass show my-token   # Dynamic: shell command run at load time; stdout becomes value.
    run:
      args:
        - -it
        - --rm
      mounts:         # Docker volume mount strings. ~ and $VAR are expanded.
        - ~/.dotfiles:/root/.dotfiles

  opencode:
    build:
      templates:
        - opencode
    run:
      mounts:
        - ~/.config/opencode:/root/.config/opencode
        - ~/.config/opencode/extensions:/root/.config/opencode/extensions:ro

  gpu:
    build:
      templates:
        - cuda
      args:
        - key: CUDA_VERSION
          value: "12.2"
    run:
      args:
        - --gpus
        - all

profiles:
  default:
    features:
      - base

  opencode-dev:
    features:
      - base
      - opencode

  gpu-dev:
    features:
      - base
      - gpu
      - opencode
```

### Profile resolution

When a profile is activated, its features are merged in list order. For each feature:

- `build.templates` are appended to the accumulated template list.
- `build.args` are appended to the accumulated args list.
- `run.args` are appended to the accumulated run args.
- `run.mounts` are appended to the accumulated mounts list.

The result is a flat build+run configuration used for that invocation.

### Dockerfile templates

Templates live in `~/.config/udocker/templates/` and use Go's [`text/template`](https://pkg.go.dev/text/template) syntax. The only variable provided is `{{ .image }}`, which holds the source image tag for that build step.

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
