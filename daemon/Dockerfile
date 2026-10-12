# daemon — passive in-sandbox HTTP server + fake pi, standalone Go module,
# builds with GOWORK=off (same pattern as controller).
FROM golang:1.25.10-alpine AS builder
WORKDIR /src
COPY daemon/go.mod ./
# no external deps yet; keep the download step for future additions
RUN true
COPY daemon/ .
RUN CGO_ENABLED=0 GOWORK=off go build -trimpath -ldflags="-s -w" -o /out/daemon .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/daemon /daemon
# Spool lives on the sandbox PVC; env/flags carry config (KAPE_SPOOL_PATH etc).
ENTRYPOINT ["/daemon"]
