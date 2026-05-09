FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY task-service/ task-service/
COPY adapters/ adapters/
COPY operator/ operator/
RUN go build -o /out/kape-task-service ./task-service/cmd/task-service/...

FROM alpine:3.20
RUN apk add --no-cache wget
COPY --from=builder /out/kape-task-service /kape-task-service
ENTRYPOINT ["/kape-task-service"]
