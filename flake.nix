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
    nix2container,
    nix-filter,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfree = true;
        };
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
        };
        nixosModules.base = {pkgs, ...}: {
          system.stateVersion = "22.05";

          # Configure networking
          networking.useDHCP = false;
          networking.interfaces.eth0.useDHCP = true;
          virtualisation.docker = {
            enable = true;
          };

          services.openssh.enable = true;

          # Create user "test"
          services.getty.autologinUser = "test";
          users.users.test.isNormalUser = true;
          users.users.test.openssh.authorizedKeys.keys = [
            "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK5iBILAktGPQc+wxxfAXAWEnyN1ygEjodzem6FLKdnH patrykwojnarowski@ringo"
          ];

          # Enable passwordless ‘sudo’ for the "test" user
          users.users.test.extraGroups = ["wheel"];
          security.sudo.wheelNeedsPassword = false;
          nix.settings.experimental-features = ["flakes" "nix-command"];

          services.k3s = {
            enable = true;
            extraFlags = [
              "--docker"
              "--container-runtime-endpoint /mnt/host/var/run/docker.sock"
            ];
          };
        };
        nixosModules.vm = {
          virtualisation.vmVariantWithBootLoader.virtualisation = {
            sharedDirectories.docksock = {
              source = "/Users/patrykwojnarowski/.orbstack/run";
              target = "/mnt/host/var/run";
              securityModel = "passthrough";
            };
            graphics = false;
            forwardPorts = [
              {
                from = "host";
                host.port = 2222;
                guest.port = 22;
              }
            ];
            vlans = [1 2];

            interfaces.eth0 = {
              vlan = 1;
              assignIP = true;
            };
          };
        };
        lib = pkgs.lib;
      in rec {
        packages = {
          operator = operator;
          operatorImage = operatorImage;
          linuxVM = nixosConfigurations.linuxVM.config.system.build.vm;
          linuxSystem = nixosConfigurations.linuxVM;
          img = lib.makeDiskImage {};
        };

        nixosConfigurations.linuxVM = nixpkgs.lib.nixosSystem {
          system = "aarch64-linux";
          modules = [
            nixosModules.base
            nixosModules.vm
            {
              virtualisation.vmVariant.virtualisation.host.pkgs = nixpkgs.legacyPackages.aarch64-darwin;
            }
          ];
        };
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            golint
            golangci-lint
            golangci-lint-langserver
            gopls
            act
            delve
            go
            # gopls
            gnumake
            tokei # loc count
            dockerfile-language-server-nodejs
            yaml-language-server
            yamlfmt
            kuttl # kubernetes tests
            kubernetes-helm
            helm-ls
            claude-code
            kubernetes-controller-tools
            dust
            nixos-rebuild
            nixos-generators
            nilaway
          ];
          shellHook = ''
            go mod tidy
          '';
          env = {
            KUBECONFIG = "/Users/patrykwojnarowski/dev/work/ipman/vm.kubeconfig";
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
