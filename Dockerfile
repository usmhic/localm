# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS build
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

COPY go.mod ./
COPY *.go ./
RUN go mod download
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w -extldflags "-static"' -o /out/localm .

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

LABEL org.opencontainers.image.title="localm" \
      org.opencontainers.image.description="Secure OpenAI-compatible gateway for local LLM runtimes" \
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
