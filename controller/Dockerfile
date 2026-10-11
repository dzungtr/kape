# Controller v1 — standalone Go module, builds with GOWORK=off.
FROM golang:1.25.10-alpine AS builder
WORKDIR /src
COPY controller/go.mod controller/go.sum ./
RUN go mod download
COPY controller/ .
RUN CGO_ENABLED=0 GOWORK=off go build -trimpath -ldflags="-s -w" -o /out/controller .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/controller /controller
# mTLS creds are mounted at runtime; env/flags carry config.
ENTRYPOINT ["/controller"]
