FROM golang:1.24.1 AS builder

WORKDIR /workspace

ENV GOCACHE=/build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/operator/ ./cmd/operator/
COPY internal ./internal
COPY pkg ./pkg
COPY api ./api


RUN --mount=type=cache,target=/build GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o manager ./cmd/operator

FROM scratch

USER 1000:1000

WORKDIR /

COPY --from=builder /workspace/manager .

ENTRYPOINT ["/manager"]


