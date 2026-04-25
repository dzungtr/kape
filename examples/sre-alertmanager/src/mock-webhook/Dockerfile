FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mock-webhook .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/mock-webhook /mock-webhook
ENTRYPOINT ["/mock-webhook"]
