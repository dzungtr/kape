# syntax=docker/dockerfile:1.6

# Builder stage
FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /out/kapeproxy ./cmd/kapeproxy

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/kapeproxy /kapeproxy
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/kapeproxy"]
