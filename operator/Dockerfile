FROM golang:1.25.10-alpine AS builder
WORKDIR /workspace
COPY operator/go.mod operator/go.sum ./
RUN go mod download
COPY operator/ .
RUN go build -o /out/kape-operator ./cmd/...

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/kape-operator /kape-operator
ENTRYPOINT ["/kape-operator"]
