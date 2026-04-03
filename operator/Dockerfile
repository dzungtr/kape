FROM golang:1.23-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY operator/ operator/
RUN go build -o /out/kape-operator ./operator/cmd/...

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/kape-operator /kape-operator
ENTRYPOINT ["/kape-operator"]
