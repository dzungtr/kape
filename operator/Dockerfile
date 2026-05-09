FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY operator/ operator/
COPY task-service/ task-service/
COPY adapters/ adapters/
RUN go build -o /out/kape-operator ./operator/cmd/...

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/kape-operator /kape-operator
ENTRYPOINT ["/kape-operator"]
