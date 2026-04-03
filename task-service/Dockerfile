FROM golang:1.23-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY task-service/ task-service/
RUN go build -o /out/kape-task-service ./task-service/cmd/task-service/...

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/kape-task-service /kape-task-service
ENTRYPOINT ["/kape-task-service"]
