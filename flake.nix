{
  description = "A very basic flake";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    nix2container.url = "github:nlewo/nix2container";
    nix-filter.url = "github:numtide/nix-filter";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    nix2container,
    nix-filter,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = import nixpkgs {inherit system;};
        n2cPkgs = nix2container.packages."${system}";

        operator = pkgs.buildGoModule {
          pname = "ipman-operator";
          src = nix-filter {
            root = ./.;
            include = [
              "internal"
              "api"
              "go.mod"
              "go.sum"
              "cmd/operator"
              "pkg/"
            ];
          };
          subPackages = ["cmd/operator"];
          version = "unversioned";
          vendorHash = "sha256-hgwtBP0SVyGEEPI/B6qAm3MdSOYFV1N9wGnm1XdkrKk=";
        };

        operatorImage = n2cPkgs.nix2container.buildImage {
          name = "plan9better/operator";
          tag = "testing";
          copyToRoot =
            pkgs.runCommand "operator-root" {
              buildInputs = [pkgs.coreutils];
            } ''
              mkdir -p $out/bin
              cp ${operator}/bin/operator $out/bin/
            '';

          # copyToRoot = pkgs.buildEnv {
          #   name = "operator-root";
          #   paths = [operator];
          # };
        };
      in {
        packages = {
          operator = operator;
          operatorImage = operatorImage;
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gnumake
            tokei
            dockerfile-language-server-nodejs
          ];
          shellHook = ''
            zsh
            go mod tidy
          '';
          env = {
            KUBECONFIG = "/Users/patrykwojnarowski/dev/work/kubeconfig";
            CHARON_POD_NAME = "charon-pod";
            XFRM_POD_NAME = "xfrm-pod";
            NAMESPACE_NAME = "ims";
            API_SOCKET_PATH = "/restctlsock/restctl.sock";
            PROXY_SOCKET_DIR = "/var/run/restctl";
            EDITOR = "hx";
          };
        };
      }
    );
}
