# Build stage: compile a static binary with the full Go toolchain.
FROM golang:1.27-alpine AS build
WORKDIR /src

# Download dependencies in their own layer, so code changes do not re-download them.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0 gives a fully static binary that runs without any C libraries.
# -trimpath and -s -w drop local paths and debug symbols, making the binary smaller.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# Run stage: only the binary. No shell, no package manager, not running as root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
