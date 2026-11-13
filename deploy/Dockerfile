# One image, two commands: api (default) and worker.
# Built from the repository root: docker build -f deploy/Dockerfile .

# The Go version matches go.mod. The build runs on the builder's own
# architecture and cross-compiles to the target one.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build
WORKDIR /src

# Modules first: this layer is rebuilt only when the dependencies change.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS
ARG TARGETARCH
# Static binaries: no libc in the final image. The time zone database is
# embedded by the commands themselves (time/tzdata).
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/api ./cmd/worker

# No shell, no package manager; CA certificates included (Auth0, Cloud SQL).
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/api /out/worker /usr/local/bin/
USER nonroot:nonroot

# The configuration comes from the environment at run time, never from the image.
# The worker is the same image with another command: worker close-weeks.
CMD ["api"]
