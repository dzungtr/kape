{
  description = "Pi dev-agent toolset — shared between host (nix develop) and the container image.";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.05";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = import nixpkgs { inherit system; };

      # The single source of truth for which tools the Pi dev-agent gets.
      # Add a tool here → host `nix develop` AND the container both pick it up.
      #
      # Deliberately EXCLUDES nodejs/bashInteractive/coreutils/git/curl: the
      # OpenShell community base (Ubuntu) ships all of them and image PATH
      # prefers /usr/bin — the nix copies were shadowed dead weight (~300MB
      # of graft). Hosts rely on system versions, same as the container.
      toolSet = with pkgs; [
        ripgrep           # rg — not shipped by the Ubuntu base
        fd                # not shipped by the Ubuntu base
        cacert            # image ENV (SSL_CERT_FILE/NIX_SSL_CERT_FILE) points at this bundle
        iproute2           # `ip` — required by OpenShell sandbox agent (network isolation helper)
        nftables           # nft — OpenShell sidecar network enforcement requires it
        iptables           # iptables-legacy — OpenShell netns enforcement fallback
        util-linux         # nsenter/mount/unshare — OpenShell sandbox agent helpers
        shadow            # groupadd/useradd for the non-root runtime user
      ];
    in
    {
      # Container consumes this: `nix profile install .#piTools`
      packages.${system} = {
        piTools = pkgs.buildEnv {
          name = "pi-tools";
          paths = toolSet;
          pathsToLink = [ "/bin" "/sbin" "/etc" ];
        };
        default = self.packages.${system}.piTools;
      };

      # Host consumes this: `nix develop` (same toolSet → identical versions)
      devShells.${system}.default = pkgs.mkShell {
        buildInputs = toolSet;
      };
    };
}
