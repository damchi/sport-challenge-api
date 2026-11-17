# One image, three commands: api (default), worker and migrate.
# Built from the repository root: docker build -f deploy/Dockerfile .

# The Go version matches go.mod. The build runs on the builder's own
# architecture and cross-compiles to the target one.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src

# Modules first: this layer is rebuilt only when the dependencies change.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
# The migrations are embedded in the migrate command.
COPY migrations ./migrations

ARG TARGETOS
ARG TARGETARCH
# Which build this is: the semantic version, the commit and its date, written
# into the binaries (internal/platform/buildinfo) and the image labels. The
# placeholders mark a build that was not made by the release.
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
# Static binaries: no libc in the final image. The time zone database is
# embedded by the commands themselves (time/tzdata).
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
    -ldflags="-s -w \
    -X github.com/damchi/sport-challenge-api/internal/platform/buildinfo.version=${VERSION} \
    -X github.com/damchi/sport-challenge-api/internal/platform/buildinfo.commit=${COMMIT} \
    -X github.com/damchi/sport-challenge-api/internal/platform/buildinfo.date=${DATE}" \
    -o /out/ ./cmd/api ./cmd/worker ./cmd/migrate

# No shell, no package manager; CA certificates included (Auth0, Cloud SQL).
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/api /out/worker /out/migrate /usr/local/bin/
USER nonroot:nonroot

# The same build facts on the image, to tell which commit an image holds
# without running it.
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
LABEL org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$COMMIT \
      org.opencontainers.image.created=$DATE

# The configuration comes from the environment at run time, never from the image.
# The worker and the migrations are the same image with another command:
# worker close-weeks, migrate up.
CMD ["api"]
