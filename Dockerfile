# One image for the gateway and the KB; compose picks the binary per service.
# Config comes from the environment (no .env is copied in). Relative paths
# (log/, docs/, .kb/, auth.json) resolve under /app, where compose mounts them.
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ \
    ./cmd/gateway ./cmd/kb ./cmd/kbtoken ./cmd/healthcheck

# distroless nonroot runs as UID/GID 65532; host dirs are owned to match.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ /app/
