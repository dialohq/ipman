FROM golang:1.24.1 AS builder

WORKDIR /workspace

ENV GOCACHE=/build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/restctl/main.go ./cmd/restctl/main.go
COPY internal ./internal
COPY pkg ./pkg
COPY api ./api


RUN --mount=type=cache,target=/build GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o restctl ./cmd/restctl

FROM ubuntu:latest

WORKDIR /

RUN apt update -y && apt install -y strongswan

COPY --from=builder /workspace/restctl .

ENTRYPOINT ["/restctl"]

