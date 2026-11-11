# dkglaze

[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`dkglaze` builds and runs personalized Docker images by layering user-specific customizations on top of an existing base image. You define **features** (named bundles of Dockerfile templates, build arguments, and run configuration) and **profiles** (ordered combinations of features) in a config file, and `dkglaze` handles chaining the builds, tagging the result, and running the final image.

A typical use case: take a standard image like `ubuntu:22.04` and automatically layer your dotfiles, user account, tools, or SSH keys on top of it — reproducibly. Switch between a minimal `default` profile and a `gpu-dev` profile with a single flag.

## Installation

### Prerequisites

To build, lint, and test dkglaze:

- `make`
- Docker with BuildKit. The Makefile sets `DOCKER_BUILDKIT=1` and runs every build, lint, and test step through `docker build` against the Dockerfile. No Go toolchain is needed on the host for these targets.

To run dkglaze:

- A `docker` CLI on `PATH` and a reachable Docker daemon (`DOCKER_HOST` is honored). `dkglaze build`/`run` shell out to `docker build`, `docker run`, and `docker exec`.
- `bash` and `sh` on the host. Shell-expanded config values run through `bash -c`, and dynamic `cmd:` values run through `sh -c`.
- A global config file at `~/.config/dkglaze/config.yml` (create it with `dkglaze config init`).

### Install

```sh
make install   # runs `make build`, then copies bin/<os>/<arch>/dkglaze to ~/.local/bin/dkglaze
```

`~/.local/bin` must already exist and be on your `PATH`.

## Usage

```
dkglaze <command> [flags]
```

| Command | Description |
|---|---|
| `build <image> [-- build-args...]` | Build the user layer on top of `<image>` (`--pull` to pull the latest base image first) |
| `run <image> [-- args...]` | Run the user-layer image (builds it first if it doesn't exist) |
| `clean [-n]` | Remove all images created by dkglaze (`-n` for dry run) |
| `list` | List all images created by dkglaze |
| `config` | Manage configuration (see below) |
| `completion <bash\|zsh>` | Print shell completion script to stdout |

### `dkglaze config` subcommands

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
dkglaze: unknown profile: "typo"

# with --verbose
dkglaze: /src/internal/config/config.go:226: /src/internal/config/config.go:115: unknown profile: typo
```

**Profile flag:** `--profile <name>` (or `-p <name>`) on `build` and `run` — overrides `defaults.profile`. Resolves against both the global config and the [project-local config](#project-local-configuration) (`./.dkglaze.yml`), if present.

**Feature flag:** `--feature <name>` (or `-f <name>`, repeatable) on `run` only — adds a named feature's `run.args`/`run.mounts` to this invocation, without declaring a whole new profile. Also resolves against the merged global + project-local feature registry. See [Project-local configuration](#project-local-configuration) below.

### Examples

```sh
dkglaze config init
dkglaze build ubuntu:22.04
dkglaze run ubuntu:22.04

# Use a non-default profile
dkglaze build --profile gpu-dev ubuntu:22.04
dkglaze run -p gpu-dev ubuntu:22.04

dkglaze clean -n   # preview what would be removed
dkglaze clean      # remove all dkglaze-managed images

# Pull the latest base image before rebuilding
dkglaze build --pull ubuntu:22.04

# Mix in ad-hoc features (e.g. repo mounts defined in ./.dkglaze.yml) without a dedicated profile
dkglaze run -p default -f repos-team-a -f repos-team-c ubuntu:22.04
```

## Shell Completion

Generate and install a completion script for your shell:

```sh
# Bash — filename must match the command name
dkglaze completion bash > ~/.local/share/bash-completion/completions/dkglaze

# Zsh — filename must be prefixed with underscore and on $fpath
dkglaze completion zsh > ~/.zsh/completions/_dkglaze
```

The zsh file **must** be named `_dkglaze` (leading underscore) to be picked up by `compinit`.

Completions are generated dynamically from the CLI model. Local Docker images are listed at
tab-press time by calling `dkglaze _list-images` (images managed by dkglaze are excluded).
Profile names (`-p`/`--profile`) and feature names (`-f`/`--feature`) are completed the same
way via `dkglaze _list-profiles` / `dkglaze _list-features`, which include both the global
config and, if present, the [project-local `.dkglaze.yml`](#project-local-configuration) for
the current directory.

## How it works

### Build

`dkglaze build <image>` resolves the active profile, merges its features in order, then runs a sequence of `docker build` invocations.

For each template in the merged template list, dkglaze:

1. Renders the `.dockerfile` template, substituting `{{ .image }}` with the current source image tag.
2. Runs `docker build --label com.dkglaze=true -t <intermediate-tag> -f <rendered-dockerfile> <context-dir>` with any merged `build.args` appended as `--build-arg` flags.
3. Uses the output image as the source for the next template.

After all templates have run, the final intermediate image is retagged as `<base>-<suffix>:<tag>` and the intermediates are removed. If any step fails, all intermediate images are cleaned up automatically.

For example, with `image = ubuntu:22.04`, `suffix = dkglaze`, and two templates `base` and `user`, dkglaze runs:

```
docker build ... -t ubuntu-dkglaze-default.0:22.04 -f base.dockerfile  # step 0: FROM ubuntu:22.04
docker build ... -t ubuntu-dkglaze-default.1:22.04 -f user.dockerfile  # step 1: FROM ubuntu-dkglaze-default.0:22.04
docker tag ubuntu-dkglaze-default.1:22.04 ubuntu-dkglaze-default:22.04 # final tag
```

Extra args can be passed directly to every `docker build` call by appending them after `--`:

```sh
dkglaze build ubuntu:22.04 -- --no-cache --build-arg FOO=bar
```

### Run

`dkglaze run <image>` resolves the active profile, merges its features, then runs:

```
docker run [merged run.args] [--volume <mount>...] [CLI args] <base>-<suffix>:<tag>
```

Mounts from `run.mounts` are expanded (`~` and `$VAR`) and passed as `--volume` flags before any CLI args. This lets you set persistent volume mounts in config while still passing one-off flags at runtime:

```sh
dkglaze run ubuntu:22.04 -- -e DEBUG=1
# executes: docker run -it --rm --volume /home/alice/.config/opencode:/root/.config/opencode -e DEBUG=1 ubuntu-dkglaze-default:22.04
```

## Configuration

Config lives at `~/.config/dkglaze/config.yml`. Run `dkglaze config init` to create it with defaults.

To use a different config directory (useful for testing or multiple configurations), set the `DKGLAZE_CONFIG_DIR` environment variable:

```sh
DKGLAZE_CONFIG_DIR=~/my-other-config dkglaze build ubuntu:22.04
```

All configuration is organized into **features** and **profiles**. There are no top-level `build` or `run` sections.

```yaml
defaults:
  suffix: dkglaze     # (required) Appended to base image name along with the active profile name.
                      # e.g. ubuntu:22.04 + suffix "dkglaze" + profile "default" → ubuntu-dkglaze-default:22.04
  profile: default    # (required) Profile used when --profile flag is not set

features:
  base:
    build:
      templates:      # Ordered list of Dockerfile template names applied in sequence.
        - base        # Resolves to ~/.config/dkglaze/templates/base.dockerfile
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

A profile can also `extends` another profile instead of (or in addition to) listing every feature itself:

```yaml
profiles:
  default:
    features: [base]
  gpu-dev:
    features: [base, gpu]
  agent-a:
    extends: gpu-dev    # resolves to [base, gpu], in order
    features: [repos-team-a]
```

`agent-a` resolves to `[base, gpu, repos-team-a]` — the extended profile's features first, then this profile's own features appended after. `extends` chains (a profile extending a profile that itself extends another) are followed to their root; a cycle (including a profile extending itself) is a hard error. This is especially useful from the [project-local config](#project-local-configuration): a local profile can `extends` a global one and just add what's local-specific, without having to know or copy the global profile's full feature list.

### Project-local configuration

For a per-project or per-workspace set of features and profiles — e.g. a tree of cloned repos where you want to mount only a task-relevant subset into the container — `dkglaze` also looks for a `.dkglaze.yml` file in the **current directory** (the directory you run `dkglaze` from, not a parent or git root). It is entirely optional and additive to the global config.

```yaml
# ./.dkglaze.yml
defaults:
  profile: agent-a  # overrides the global config's defaults.profile when -p is not given

features:
  repos-team-a:
    run:
      mounts:
        - $PWD/team-a/service-x:$PWD/team-a/service-x
        - $PWD/team-b/lib-y:$PWD/team-b/lib-y

  repos-team-c:
    run:
      mounts:
        - $PWD/team-c/service-z:$PWD/team-c/service-z

profiles:
  agent-a:
    extends: gpu-dev    # a profile from the global config — inherits its full feature list
    features:
      - repos-team-a    # a feature defined locally, above
```

```sh
cd ~/projects   # workspace root containing team-a/, team-b/, team-c/...

# no -p needed — ./.dkglaze.yml's defaults.profile selects agent-a
dkglaze run myimage

# a named, reusable local profile — tab-completable, mixes global + local features
dkglaze run -p agent-a myimage

# or compose ad hoc, on top of a plain global profile
dkglaze run -p default myimage -f repos-team-a -f repos-team-c
```

A few rules keep this predictable and safe:

- **Discovery is current-directory only** — no walking up to a parent or git root. Set `DKGLAZE_LOCAL_CONFIG=/path/to/file.yml` to point at a different file explicitly; if that variable is set and the file is missing, `dkglaze` errors (whereas a missing default `./.dkglaze.yml` is simply ignored).
- **Local features cannot define `build:`** — only `run.args`/`run.mounts`. A `build:` key under a local feature is a parse error, so a shared, cloned `.dkglaze.yml` cannot change what gets built.
- **Local `run.args`/`run.mounts` are not shell-expanded by default** — `dkglaze build`/`dkglaze run` refuse to expand them and error (naming the feature and file) unless you pass `--trust` on that invocation. See [Security model](#security-model).
- **Names must not collide** with the global config. If a local feature or profile shares a name with a global one, `dkglaze` errors rather than silently shadowing it.
- **Local profiles can mix local and global features** (as in the `agent-a` example above) and work for both `dkglaze build -p <name>` and `dkglaze run -p <name>`, exactly like global profiles.
- **A local (or global) profile can `extends` another profile** — see [Profile resolution](#profile-resolution) — so a local profile only needs to list what it adds on top of an existing (typically global) profile, instead of duplicating its whole feature list.
- **`defaults.profile` in `./.dkglaze.yml` overrides the global config's `defaults.profile`** when `-p` is not given, letting a project pick its own default profile without touching the global config. Precedence is: `-p` flag > local `defaults.profile` > global `defaults.profile`. It must name a profile that exists (local or global) once merged, or `dkglaze` errors.
- **`-f/--feature <name>` is `run`-only** and applies only a feature's `run.args`/`run.mounts` — its `build.*` is ignored (with a warning if present), since the image tag is derived from the *profile* name, not from `-f` selections. This means one built image is reused no matter which `-f` combination you pass at runtime.
- **Local-sourced selections are never silent.** Whenever `run` resolves a profile or feature that came from `./.dkglaze.yml` — including a profile picked only because of local `defaults.profile`, or a profile reached via an `extends` chain — it prints a warning naming it and the local file path, so you always know when a run depends on something outside the global config. `-n`/dry-run still prints the exact resulting command either way.
- Both local and global feature/profile names are tab-completable (see [Shell Completion](#shell-completion)).

### Security model

dkglaze treats its two config sources differently:

- **Global config (`~/.config/dkglaze/config.yml`) is fully trusted.** It is your own file. Its `run.args` and `run.mounts` values are shell-expanded through `bash -c`, so command substitution such as `$(...)` runs as written. Dynamic `build.args` `cmd:` entries run through `sh -c`. This is intentional.
- **Project-local config (`./.dkglaze.yml`) is treated as untrusted input**, since it may come from a repository you cloned:
  - Local features cannot define `build:` at all. There is no field for it.
  - Local features' `run.args` and `run.mounts` are **not** shell-expanded by default. If a `build` or `run` resolves a local feature with non-empty `run.args` or `run.mounts` (including via `-f`), dkglaze stops with an error naming the feature and the file.
  - To proceed, pass `--trust` on that one invocation, for example `dkglaze run --trust -p agent-a myimage`. This is the only way local `run.args`/`run.mounts` get shell-expanded.
  - There is deliberately no short flag and no environment variable or config setting for this. The opt-in is typed out each time and applies to that command only.
- **Only pass `--trust` for a `.dkglaze.yml` you actually trust.** Read the file first: with `--trust`, any `$(...)` or backtick expression in it runs with your privileges.

### Dockerfile templates

Templates live in `~/.config/dkglaze/templates/` and use Go's [`text/template`](https://pkg.go.dev/text/template) syntax. The only variable provided is `{{ .image }}`, which holds the source image tag for that build step.

```dockerfile
# ~/.config/dkglaze/templates/base.dockerfile
FROM {{ .image }}
RUN apt-get update && apt-get install -y vim git

# ~/.config/dkglaze/templates/user.dockerfile
FROM {{ .image }}
ARG USERNAME
RUN useradd -m $USERNAME
```

**Build context:** if a directory `~/.config/dkglaze/templates/<name>/` exists, it is used as the Docker build context for that step (useful for `COPY` instructions). Otherwise the `templates/` directory itself is used.

Every image built by `dkglaze` is labeled `com.dkglaze=true`, which is how `dkglaze clean` finds and removes them.

## Development

All build, lint, and test targets run inside Docker via BuildKit. Only `make` and `docker` are required on the host for these targets. The exception is `unit-test-cover`, which runs `go tool cover` on the host and so needs a Go toolchain.

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

## License

dkglaze is released under the [MIT License](LICENSE).
