FROM golang:1.25.10-alpine AS builder
WORKDIR /workspace
COPY task-service/go.mod task-service/go.sum ./
RUN go mod download
COPY task-service/ .
RUN go build -o /out/kape-task-service ./cmd/task-service/...

FROM alpine:3.20
RUN apk add --no-cache wget
COPY --from=builder /out/kape-task-service /kape-task-service
ENTRYPOINT ["/kape-task-service"]
