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
              "cmd/operator"
            ];
          };
          subPackages = ["cmd/operator"];
          version = "unversioned";
          vendorHash = "sha256-yh8Rle3LFeokRsokzKAV2/ivuaIjQZMsW9errFKvxxM=";
        };

        operatorImage = n2cPkgs.nix2container.buildImage {
          name = "plan9better/operator";
          tag = "testing";
          copyToRoot = operator;
        };
        test = pkgs.runCommand "mytest" {} ''
          mkdir -p $out/bin/
          touch $out/bin/file
          ls -lah > $out/bin/file
        '';
        testImage = n2cPkgs.nix2container.buildImage {
          name = "somefkntest";
          tag = "testing";
          copyToRoot = "${pkgs.tcpdump}/bin/tcpdump";
        };
      in {
        packages = {
          test = test;
          testImage = testImage;
          operator = operator;
          operatorImage = operatorImage;
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gnumake
          ];
          shellHook = ''
            zsh
            go mod tidy
          '';
          env.KUBECONFIG = "/Users/patrykwojnarowski/dev/work/kubeconfig";
        };
      }
    );
}
