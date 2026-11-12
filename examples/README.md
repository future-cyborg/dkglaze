# Examples

Real-world `features:` from a working dkglaze config, for common patterns:
a plain interactive shell, mounting the current directory, running as your
host user instead of root, and sharing tool config/caches with a container.

See [`docs/CONFIGURATION.md`](../docs/CONFIGURATION.md) for the full schema.

## `base` — interactive shell

```yaml
base:
  run:
    args:
      - "--rm"
      - "-it"
      - "-e TERM=xterm-256color"
      - "--workdir=$PWD"
      - "--entrypoint bash"
      - "--network=host"
```

Drops you into a throwaway (`--rm`) interactive shell with working colors,
the container's working directory matching your host cwd, and host
networking so container processes can reach services on localhost.

## `mount` — bind-mount the current directory

```yaml
mount:
  run:
    args:
      - "--workdir=$PWD"
    mounts:
      - $PWD:$PWD
```

Mounts your current directory into the container at the same path, so tools
that print or expect absolute paths behave the same inside and out. Kept
separate from `base` so it can be left out of a profile when you don't want
the container touching files on disk (see [Combining into a
profile](#combining-into-a-profile) below).

## `sudo-user` — run as your host user, with passwordless sudo

```yaml
sudo-user:
  build:
    templates: [sudo]
  run:
    args:
      - "-u $(id -u):$(id -g)"
    mounts:
      - /etc/passwd:/etc/passwd:ro
      - /etc/group:/etc/group:ro
      - ~/docker/.bashrc:/home/$(id -un)/.bashrc:ro
      - ~/docker/.bash_history:/home/$(id -un)/.bash_history:rw
      - ~/.local/bin:/home/$(id -un)/.local/bin:ro
      - ~/.gitconfig:/home/$(id -un)/.gitconfig:ro
```

```dockerfile
# templates/sudo.dockerfile
FROM {{ .image }}
RUN apt-get update && apt-get install -y sudo \
    && rm -rf /var/lib/apt/lists/* \
    && echo "ALL ALL=(ALL) NOPASSWD: ALL" >> /etc/sudoers \
    && echo -e '#%PAM-1.0\nauth       sufficient   pam_permit.so\naccount    sufficient   pam_permit.so\nsession    sufficient   pam_permit.so' > /etc/pam.d/sudo
```

The template bakes in passwordless `sudo` at build time. `-u $(id -u):$(id -g)`
runs the container as your host UID/GID instead of root, so files it
creates (e.g. under a bind mount) are owned by you on the host. Bind-mounting
`/etc/passwd`/`/etc/group` read-only makes that UID resolve to a real
username (`$(id -un)`) inside the container instead of showing up as a
numeric ID. The remaining mounts carry over a shell history/config, your
`~/.local/bin`, and `~/.gitconfig`.

## `opencode` — share OpenCode config/cache/state

```yaml
opencode:
  run:
    args:
      - "-e NPM_CONFIG_CACHE=/tmp/npm-cache"
    mounts:
      - ~/.config/opencode:/home/$(id -un)/.config/opencode:rw
      - ~/.cache/opencode:/home/$(id -un)/.cache/opencode:rw
      - ~/.local/share/opencode:/home/$(id -un)/.local/share/opencode:rw
      - ~/.local/state/opencode:/home/$(id -un)/.local/state/opencode:rw
      - ~/.local/share/opencode-working-memory:/home/$(id -un)/.local/share/opencode-working-memory
      - ~/.config/gcloud/application_default_credentials.json:/home/$(id -un)/.config/gcloud/application_default_credentials.json:ro
```

Mounts [OpenCode](https://opencode.ai)'s config, cache, and state directories
so sessions, auth, and history carry over between containers instead of
starting fresh each time. `NPM_CONFIG_CACHE` is redirected to `/tmp` to
avoid permission conflicts between the host-owned npm cache and the
container's mapped user. The gcloud credentials mount is specific to this
setup's cloud auth — drop it if you don't need it.

## `docker` — Docker-in-container via the host socket

```yaml
docker:
  build:
    templates: [docker]
  run:
    args:
      - "--group-add $(getent group docker | cut -d: -f3)"
    mounts:
      - /var/run/docker.sock:/var/run/docker.sock
      - ~/.docker:/home/$(id -un)/.docker
```

```dockerfile
# templates/docker.dockerfile
FROM {{ .image }}
RUN DEBIAN_FRONTEND=noninteractive apt-get -qq update \
    && apt-get install -qq -y \
    docker.io \
    docker-buildx \
    && rm -rf /var/lib/apt/lists/*
```

The template installs the `docker` CLI and buildx into the image.
Bind-mounting the host's Docker socket lets that CLI talk to the host
daemon instead of needing its own (no Docker-in-Docker). `--group-add`
adds the container user to a group matching the host's `docker` group GID,
since the socket is normally only accessible to that group. Mounting
`~/.docker` carries over any client config (e.g. `buildx` builder
definitions) from the host.

## Combining into a profile

Profiles just pick which features apply. Pairing `extends` with a small
"everything except X" feature lets one profile opt out of something the
rest share — here, `default` is `default-no-mount` plus `mount`, so a
project that doesn't want its directory bind-mounted can use
`default-no-mount` directly:

```yaml
profiles:
  default:
    extends: default-no-mount
    features:
      - mount
  default-no-mount:
    features:
      - base
      - sudo-user
      - opencode
      - docker
```
