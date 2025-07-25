{
  description = "A very basic flake";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    nix2container.url = "github:nlewo/nix2container";
    nix-filter.url = "github:numtide/nix-filter";
    nixidy.url = "github:dialohq/nixidy/d010752e7f24ddaeedbdaf46aba127ca89d1483a";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    # nix2container,
    # nix-filter,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfree = true;
        };
      in {
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
