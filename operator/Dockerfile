FROM golang:1.25.10-alpine AS builder
WORKDIR /workspace
# The operator imports the OpenShell client generated into the controller module
# (handoff H3: each Go slice imports github.com/dzungtr/kape/controller/gen), so
# the module is part of this build context via the replace in operator/go.mod.
COPY controller/ /controller/
COPY operator/go.mod operator/go.sum ./
RUN go mod download
COPY operator/ .
RUN CGO_ENABLED=0 go build -o /out/kape-operator ./cmd

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/kape-operator /kape-operator
ENTRYPOINT ["/kape-operator"]
