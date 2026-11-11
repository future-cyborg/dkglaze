# syntax = docker/dockerfile:1-experimental
#
# The "syntax" line above must be the first line as it defines the syntax used
# in this file.
#
# https://docs.docker.com/engine/reference/builder/#automatic-platform-args-in-the-global-scope

ARG BASE_IMAGE
ARG LINT_IMAGE

#-------------------------------
FROM ${LINT_IMAGE} AS lint-base

#-------------------------------
FROM --platform=${BUILDPLATFORM} ${BASE_IMAGE} AS base
WORKDIR /src
ENV CGO_ENABLED=0
# GOPRIVATE tells Go to fetch matching modules directly via git instead of
# the public proxy. Required for private modules; optional for public repos.
ENV GOPRIVATE=
# Uncomment the lines below if your modules require authentication (private repos):
# ARG VCS_LOGIN
# ARG VCS_TOKEN
# RUN echo "machine github.com login ${VCS_LOGIN} password ${VCS_TOKEN}" > ~/.netrc
COPY go.* .
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download all

#-------------------------------
FROM base AS tidy-runner
RUN --mount=rw,target=. \
    --mount=type=cache,target=/go/pkg/mod \
    go mod tidy && \
    mkdir -p /out && cp /src/go.* /out/

FROM scratch AS run-tidy
COPY --from=tidy-runner /out/* /

#-------------------------------
FROM base AS format-runner-base
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/daixiang0/gci && \
    go install mvdan.cc/gofumpt

FROM format-runner-base AS format-runner
ARG REPO_NAME
RUN --mount=target=.,rw \
    mkdir -p cmd internal pkg test /out/cmd /out/internal /out/pkg /out/test && \
    find cmd internal pkg test -type f -name '*.go' \
        | while read -r x ; do \
            mkdir -p "/out/$(dirname $x)" ; \
            cp "$x" "/out/$(dirname $x)/" ; \
        done && \
    find /out/cmd /out/internal /out/pkg /out/test -type f -name '*.go' \
        | while read -r x ; do \
            gofumpt -w $x ; \
            gci write -s Standard -s Default -s "Prefix(${REPO_NAME})" $x ; \
        done

FROM scratch AS run-format
COPY --from=format-runner /out/ /

#-------------------------------
FROM base AS generate-runner-base
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install mvdan.cc/gofumpt
    # PLACEHOLDER - add any tools needed for go generate here

FROM generate-runner-base AS generate-runner
RUN --mount=target=.,rw \
    go generate ./internal/... ./pkg/... ./test/... && \
    find internal pkg test -type f -name '*.go' \
        | while read -r x ; do \
            mkdir -p "/out/$(dirname $x)" ; \
            cp "$x" "/out/$(dirname $x)/" ; \
        done

FROM scratch AS run-generate
COPY --from=generate-runner /out/ /

#-------------------------------
FROM base AS lint-runner
COPY .golangci.yml .
RUN --mount=target=.,rw \
    --mount=from=lint-base,src=/usr/bin/golangci-lint,target=/usr/bin/golangci-lint \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint \
    mkdir -p cmd internal pkg test /out/cmd /out/internal /out/pkg /out/test && \
    mkdir -p /out && golangci-lint run \
        ./cmd/... \
        ./internal/... \
        ./pkg/... \
        > /out/lint-run ; echo "$?" > /out/lint-run.exitcode

FROM scratch AS run-linters
COPY --from=lint-runner /out/ /

#-------------------------------
FROM base AS unit-test-runner
ARG TEST_PATH
ARG TEST_FLAGS
RUN --mount=target=. \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out && \
        go test ${TEST_FLAGS} -v -covermode=count -coverprofile=/out/unit-test-run.cover.out \
            ${TEST_PATH} \
            > /out/unit-test-run; echo "$?" > /out/unit-test-run.exitcode

FROM scratch AS run-unit-tests
COPY --from=unit-test-runner /out/* /

#-------------------------------
FROM base AS bench-test-runner
ARG TEST_PATH
ARG TEST_FLAGS
RUN --mount=target=. \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out && \
        go test ${TEST_FLAGS} -v -bench=. \
            ${TEST_PATH} \
            > /out/bench-test-run; echo "$?" > /out/bench-test-run.exitcode

FROM scratch AS run-bench-tests
COPY --from=bench-test-runner /out/* /

#-------------------------------
FROM base AS build-bin-runner
ARG TARGETOS
ARG TARGETARCH
ARG BUILD
ARG BUILD_FLAGS
ARG VERSION
RUN --mount=target=. \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build ${BUILD_FLAGS} -ldflags "-X main.version=${VERSION}" -o /out/${BUILD} ./cmd/${BUILD}

FROM scratch AS build-bin
ARG BUILD
COPY --from=build-bin-runner /out/${BUILD} /

#-------------------------------
FROM scratch AS build-docker-image
ARG BUILD
COPY --from=build-bin-runner /out/${BUILD} /run/service
ENTRYPOINT ["/run/service"]