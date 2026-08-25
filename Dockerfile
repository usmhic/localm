# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.27.0-alpine AS build
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY *.go ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w -extldflags "-static"' -o /out/localm .

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

LABEL org.opencontainers.image.title="localm" \
      org.opencontainers.image.description="Secure OpenAI-compatible gateway for local, self-hosted, and remote LLM providers" \
      org.opencontainers.image.source="https://github.com/usmhic/localm" \
      org.opencontainers.image.authors="usmhic" \
      org.opencontainers.image.licenses="MIT"

WORKDIR /
COPY --from=build /out/localm /localm

ENV LISTEN_ADDR=:8080
EXPOSE 8080

USER nonroot:nonroot
STOPSIGNAL SIGTERM
ENTRYPOINT ["/localm"]
