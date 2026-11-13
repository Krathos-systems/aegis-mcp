# aegis-mcp in a container. It speaks MCP over stdio, so run it with stdin
# attached and no TTY: `docker run -i --rm …` or `docker compose run --rm -T …`.

FROM --platform=$BUILDPLATFORM golang:1.26.3 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY assets/aegis-mark.svg assets/aegis-mark-256.png ./assets/
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.version=${VERSION}" -o /out/aegis-mcp .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/aegis-mcp /aegis-mcp
# No shell, no package manager, not root.
USER 65532:65532
ENTRYPOINT ["/aegis-mcp"]

LABEL org.opencontainers.image.title="aegis-mcp" \
      org.opencontainers.image.description="MCP server for Aegis" \
      org.opencontainers.image.source="https://github.com/Krathos-systems/aegis-mcp" \
      org.opencontainers.image.licenses="Apache-2.0"
