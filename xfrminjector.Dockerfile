FROM golang:1.24.1 AS builder

WORKDIR /workspace

ARG PLATFORM=arm64

ENV GOCACHE=/build
COPY ./goviciclient ./goviciclient
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/xfrminjector/ ./cmd/xfrminjector/
COPY internal ./internal
COPY pkg ./pkg
COPY api ./api


RUN --mount=type=cache,target=/build GOOS=linux GOARCH=${PLATFORM} CGO_ENABLED=0 go build -o injector ./cmd/xfrminjector

FROM ubuntu:latest

WORKDIR /

COPY --from=builder /workspace/injector .


ENTRYPOINT ["/injector"]


