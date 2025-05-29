FROM nixos/nix

RUN nix-env -iA nixpkgs.strongswan
RUN mkdir /var/run && touch /etc/strongswan.conf
CMD ["bash", "-c", "$(find /nix/store -name 'charon')"]
