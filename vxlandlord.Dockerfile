FROM golang:1.24.1 AS builder

WORKDIR /workspace

ENV GOCACHE=/build
COPY ./goviciclient ./goviciclient
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/vxlandlord/ ./cmd/vxlandlord/
COPY internal ./internal
COPY pkg ./pkg
COPY api ./api

RUN --mount=type=cache,target=/build GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o vxlandlord ./cmd/vxlandlord

FROM ubuntu:latest

RUN apt update -y && apt install -y iproute2

WORKDIR /

COPY --from=builder /workspace/vxlandlord .

ENTRYPOINT ["/vxlandlord"]

