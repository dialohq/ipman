FROM golang:1.24.1 AS builder

WORKDIR /workspace
COPY go.mod go.mod
RUN go mod download

COPY main.go main.go

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o xfrminion main.go

FROM nixos/nix:2.15.0
# TODO: for prod, build on linux/amd64 to avoid this issue
# https://github.com/DeterminateSystems/nix-installer/issues/324
# MacOS runs docker in QEMU, which doesn't allow syscall filtering
# with SecComp, apparently its only so the build system doesn't create
# a setuid binary so not a big deal: https://github.com/DeterminateSystems/nix-installer/issues/324#issuecomment-1491888268 
RUN mkdir -p /etc/nix && echo "filter-syscalls = false" > /etc/nix/nix.conf


RUN nix-env -iA  \
    nixpkgs.bash \
    nixpkgs.toybox \
    nixpkgs.iproute2

COPY --from=builder /workspace/xfrminion /usr/local/bin/xfrminion

ENTRYPOINT ["/usr/local/bin/xfrminion"]

