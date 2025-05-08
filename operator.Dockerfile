FROM golang:1.24.1 AS builder

WORKDIR /workspace

COPY go.mod ./
COPY cmd/operator/main.go ./cmd/operator/main.go
COPY internal ./internal
COPY pkg ./pkg
COPY api ./api
RUN go mod tidy
RUN go mod download


RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o manager ./cmd/operator

FROM gcr.io/distroless/static-debian11

USER 1000:1000

WORKDIR /

COPY --from=builder /workspace/manager .

ENTRYPOINT ["/manager"]

