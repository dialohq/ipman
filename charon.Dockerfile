FROM nixos/nix

RUN mkdir -p /etc/nix && echo "filter-syscalls = false" > /etc/nix/nix.conf
RUN nix-env -iA nixpkgs.strongswan
RUN mkdir /var/run && touch /etc/strongswan.conf
CMD ["bash", "-c", "$(find /nix/store -name 'charon')"]
