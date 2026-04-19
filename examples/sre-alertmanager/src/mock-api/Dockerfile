FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mock-api .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/mock-api /mock-api
ENTRYPOINT ["/mock-api"]
