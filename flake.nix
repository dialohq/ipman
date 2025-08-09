{
  description = "A very basic flake";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    nix2container.url = "github:nlewo/nix2container";
    nix-filter.url = "github:numtide/nix-filter";
    nixidy.url = "github:dialohq/nixidy/d010752e7f24ddaeedbdaf46aba127ca89d1483a";
    build-go-cache.url = "github:numtide/build-go-cache";
    build-go-cache.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    build-go-cache,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfree = true;
        };
      in {
        packages = let
          proxyVendor = true;
          vendorHash = "sha256-mYcO8t+4O0J0kO+H9wCFSHzHpHPTwwFcaWEP6dytQVA=";
          src = ./.;
          goCache = build-go-cache.legacyPackages.${system}.buildGoCache {
            importPackagesFile = ./imported-packages;
            inherit proxyVendor vendorHash src;
          };
        in {
          operator = pkgs.buildGoModule {
            subPackages = ["cmd/operator"];
            name = "ipman-operator";
            buildInputs = [goCache];
            inherit proxyVendor src vendorHash;
            doCheck = false;
            env = {
              CGO_ENABLED = 0;
            };
          };
          xfrminion = pkgs.buildGoModule {
            subPackages = ["cmd/xfrminion"];
            name = "ipman-xfrminion";
            buildInputs = [goCache];
            inherit proxyVendor src vendorHash;
          };
        };
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            golint
            golangci-lint
            golangci-lint-langserver
            gopls
            delve
            go
            nilaway

            act
            gnumake
            tokei # loc count
            kuttl # kubernetes tests
            kubernetes-helm
            helm-ls
            claude-code
            kubernetes-controller-tools
            dust
            nixos-rebuild
            nixos-generators
            mods

            dockerfile-language-server-nodejs
            yaml-language-server
            yamlfmt
          ];
          shellHook = ''
            go mod tidy
          '';
          env = {
            KUBECONFIG = "./kubeconfig";
          };
        };
      }
    );
}
