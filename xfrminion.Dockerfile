FROM golang:1.24.1 AS builder

WORKDIR /workspace

ENV GOCACHE=/build
COPY go.mod go.sum ./
COPY ./goviciclient ./goviciclient
RUN go mod download
COPY cmd/xfrminion/ ./cmd/xfrminion/
COPY internal ./internal
COPY pkg ./pkg
COPY api ./api


RUN --mount=type=cache,target=/build GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o xfrminion ./cmd/xfrminion

FROM ubuntu:latest

RUN apt update -y && apt install -y iproute2 tcpdump iputils-ping

WORKDIR /

COPY --from=builder /workspace/xfrminion .

ENTRYPOINT ["/xfrminion"]

