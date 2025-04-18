FROM golang:1.23.7 AS builder

WORKDIR /workspace
COPY go.mod go.mod
RUN go mod download

COPY main.go main.go

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o restctl main.go

FROM nixos/nix:2.15.0
# TODO: for prod, build on linux/amd64 to avoid this issue
# https://github.com/DeterminateSystems/nix-installer/issues/324
# MacOS runs docker in QEMU, which doesn't allow syscall filtering
# with SecComp, apparently its only so the build system doesn't create
# a setuid binary so not a big deal: https://github.com/DeterminateSystems/nix-installer/issues/324#issuecomment-1491888268 
RUN mkdir -p /etc/nix && echo "filter-syscalls = false" > /etc/nix/nix.conf


RUN nix-env -iA \
    nixpkgs.strongswan \
    nixpkgs.bash

COPY --from=builder /workspace/restctl /usr/local/bin/restctl

ENTRYPOINT ["/usr/local/bin/restctl"]

