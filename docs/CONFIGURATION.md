# Configuration Reference

For complete examples, see [`examples/`](../examples/).

Config lives at `~/.config/dkglaze/config.yml`. Run `dkglaze config init` to create it with defaults.

To use a different config directory (useful for testing or multiple configurations), set the `DKGLAZE_CONFIG_DIR` environment variable:

```sh
DKGLAZE_CONFIG_DIR=~/my-other-config dkglaze build ubuntu:26.04
```

The global config always has a top-level `defaults:` block:

```yaml
defaults:
  suffix: dkglaze     # (required) Appended to base image name along with the active profile name.
                      # e.g. ubuntu:26.04 + suffix "dkglaze" + profile "default" → ubuntu-dkglaze-default:26.04
  profile: default    # (required) Profile used when --profile flag is not set
```

All configuration is organized into **features** and **profiles**. There are no top-level `build` or `run` sections.

## Features

A **feature** is a named, reusable bundle of build and/or run configuration (templates, build args, run args/mounts).

```yaml
features:
  base:
    build:
      templates:      # Ordered list of Dockerfile template names, each run in sequence on top of the previous step's result.
        - base        # Resolves to ~/.config/dkglaze/templates/base.dockerfile
        - user        # Resolves to ~/.config/dkglaze/templates/user.dockerfile
      args:
        - key: USERNAME
          value: alice              # Static value — mutually exclusive with `cmd:`. Passed to every template step above, so each one needs its own `ARG USERNAME` to use it.
        - key: GIT_SHA
          cmd: git rev-parse HEAD   # Dynamic: shell command run during `build` (via `sh -c`, 30s timeout); stdout becomes the value.
    run:
      args:
        - -it         # dkglaze doesn't add any run flags of its own — include -it/--rm yourself if you want them.
        - --rm
      mounts:         # Docker volume mount strings. ~ and $VAR are expanded.
        - ~/.dotfiles:/home/alice/.dotfiles

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
```

`build.args` values (static or `cmd:`-derived) are passed to Docker as `--build-arg` and appear in plain text in dkglaze's log output and in `docker history` of the built image. Don't put secrets in them — use [BuildKit secret mounts](https://docs.docker.com/build/building/secrets/) in your template instead.

**How `run.args`/`run.mounts` are expanded:** each entry is run through a shell (see [Security model](#security-model) for which configs are trusted to do this). `run.args` entries are word-split and glob-expanded like a shell command line — e.g. a single entry `--gpus all` becomes **two** args (write it as two list entries, `- --gpus` / `- all`, as above), and a literal `*` expands against files in the current directory. `run.mounts` entries are expanded as a whole (`~`, `$VAR`, `$(...)`) but not word-split, so a single mount string stays one `--volume` value — just don't leave unquoted spaces in the path.

## Profiles

A **profile** is a named, ordered list of features to activate together for a given `dkglaze build`/`run` invocation.

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
  gpu-dev-via-extends:
    extends: default    # resolves to [base]
    features: [gpu]
```

`gpu-dev-via-extends` resolves to `[base, gpu]` — the same result as `gpu-dev` above, just expressed by extending `default` and adding `gpu` on top: the extended profile's features come first, then this profile's own features are appended after. `extends` chains (a profile extending a profile that itself extends another) are followed to their root; a cycle (including a profile extending itself) is a hard error. This is especially useful for [project-local config](#project-local-configuration) below: a local profile can `extends` a global one and just add what's local-specific, without having to know or copy the global profile's full feature list.

## Project-local configuration

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

`repos-team-a`/`repos-team-c` have local `run.mounts`, so every command below needs `--trust` (see [Security model](#security-model)):

```sh
cd ~/projects   # workspace root containing team-a/, team-b/, team-c/...

# no -p needed — ./.dkglaze.yml's defaults.profile selects agent-a
dkglaze run --trust myimage

# a named, reusable local profile — tab-completable, mixes global + local features
dkglaze run --trust -p agent-a myimage

# or compose ad hoc, on top of a plain global profile
dkglaze run --trust -p default -f repos-team-a -f repos-team-c myimage
```

A few rules keep this predictable and safe:

- **Discovery is current-directory only** — no walking up to a parent or git root. Set `DKGLAZE_LOCAL_CONFIG=/path/to/file.yml` to point at a different file explicitly; if that variable is set and the file is missing, `dkglaze` errors (whereas a missing default `./.dkglaze.yml` is simply ignored).
- **Local features cannot define `build:`** — only `run.args`/`run.mounts`. A `build:` key under a local feature is a parse error, so a shared, cloned `.dkglaze.yml` cannot change what gets built.
- **Local `run.args`/`run.mounts` are not shell-expanded by default** — `dkglaze build`/`dkglaze run` refuse to expand them and error (naming the feature and file) unless you pass `--trust` on that invocation. See [Security model](#security-model).
- **Names must not collide** with the global config. If a local feature or profile shares a name with a global one, `dkglaze` errors rather than silently shadowing it.
- **Local profiles can mix local and global features** (as in the `agent-a` example above) and work for both `dkglaze build -p <name>` and `dkglaze run -p <name>`, exactly like global profiles.
- **A local (or global) profile can `extends` another profile** — see [Profiles](#profiles) — so a local profile only needs to list what it adds on top of an existing (typically global) profile, instead of duplicating its whole feature list.
- **`defaults.profile` in `./.dkglaze.yml` overrides the global config's `defaults.profile`** when `-p` is not given, letting a project pick its own default profile without touching the global config. Precedence is: `-p` flag > local `defaults.profile` > global `defaults.profile`. It must name a profile that exists (local or global) once merged, or `dkglaze` errors. Local `defaults:` only supports `profile` — there is no local `suffix` override, and a `suffix:` key under local `defaults:` is a strict-decoding parse error (unlike the global config, unknown keys anywhere in `.dkglaze.yml` are rejected).
- **`-f/--feature <name>` is `run`-only** and applies only a feature's `run.args`/`run.mounts` — its `build.*` is ignored (with a warning if present), since the image tag is derived from the *profile* name, not from `-f` selections. This means one built image is reused no matter which `-f` combination you pass at runtime.
- **Local-sourced selections are never silent.** Whenever `run` resolves a profile or feature that came from `./.dkglaze.yml` — including a profile picked only because of local `defaults.profile`, or a profile reached via an `extends` chain — it prints a warning naming it and the local file path, so you always know when a run depends on something outside the global config. This also applies with `-n`/dry-run — but dry-run does **not** bypass the `--trust` check above: an untrusted local feature with non-empty `run.args`/`run.mounts` still errors before anything is printed, so `-n` can't be used to safely preview a `.dkglaze.yml` you haven't trusted yet.
- `dkglaze config list`, `config validate`, and `config show` read the global config only — they never merge `./.dkglaze.yml`. Use `dkglaze build`/`run` (or `completion`) to see local profiles/features take effect. `config show --resolved` additionally resolves **every** global profile to print it, which runs every `cmd:` build arg and shell-expands every profile's `run.args`/`run.mounts` — not just the one you're interested in.
- Both local and global feature/profile names are tab-completable (see the README's [Shell Completion](../README.md#shell-completion) section).

## Security model

dkglaze treats its two config sources differently:

- **Global config (`~/.config/dkglaze/config.yml`) is fully trusted.** It is your own file. Its `run.args` and `run.mounts` values are shell-expanded through `bash -c`, so command substitution such as `$(...)` runs as written. Dynamic `build.args` `cmd:` entries run through `sh -c`. This is intentional.
- **Project-local config (`./.dkglaze.yml`) is treated as untrusted input**, since it may come from a repository you cloned:
  - Local features cannot define `build:` at all. There is no field for it.
  - Local features' `run.args` and `run.mounts` are **not** shell-expanded by default. If a `build` or `run` resolves a local feature with non-empty `run.args` or `run.mounts` (including via `-f`), dkglaze stops with an error naming the feature and the file.
  - To proceed, pass `--trust` on that one invocation, for example `dkglaze run --trust -p agent-a myimage`. This applies to `dkglaze build` too — e.g. `dkglaze build --trust -p agent-a myimage` — since build also resolves and shell-expands the profile's run values, even though it doesn't use them. This is the only way local `run.args`/`run.mounts` get shell-expanded.
  - There is deliberately no short flag and no environment variable or config setting for this. The opt-in is typed out each time and applies to that command only.
- **Only pass `--trust` for a `.dkglaze.yml` you actually trust.** Read the file first: with `--trust`, any `$(...)` or backtick expression in it runs with your privileges.

## Dockerfile templates

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
