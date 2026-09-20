{
  description = "KAPE (Kubernetes Agentic Platform Execution) development environment";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      pkgsFor = system: nixpkgs.legacyPackages.${system};

      # Toolchain shared by devShell and the container image.
      # Mirrors CONTRIBUTING.md prerequisites.
      devTools = pkgs: with pkgs; [
        # Go workspace (go.work: go 1.25; 1.26 toolchain is fully compatible —
        # go_1_25 is EOL/removed from nixpkgs)
        go_1_26
        golangci-lint
        # controller-gen: not in nixpkgs — installed via `go install` in Containerfile.dev
        # Cluster tooling
        kubectl
        kind
        kubernetes-helm
        # Python runtime/ (uv manages exact interpreter + deps via uv.lock)
        python313
        uv
        ruff
        # dashboard/ (bun — never npm/yarn)
        bun
        # Containers — podman, never docker
        podman
        skopeo
        # Common dev
        git
        gh
        gnumake
        gcc
        pkg-config
        bashInteractive
        curl
        jq
        less
        glibcLocales
        cacert
      ];
    in
    {
      packages = forAllSystems (system:
        let pkgs = pkgsFor system; in
        rec {
          # Profile-installable toolset. Installed ON TOP of the default nix
          # profile in ghcr.io/dzungtr/pi-openshell (piTools) — see
          # Containerfile.dev. Collisions resolve to piTools (lower priority
          # number wins; this installs with --priority 5).
          kapeTools = pkgs.buildEnv {
            name = "kape-dev-env";
            paths = devTools pkgs;
            extraOutputsToInstall = [ "man" ];
            pathsToLink = [ "/bin" "/etc" "/share" ];
          };

          default = kapeTools;
        });

      devShells = forAllSystems (system:
        let pkgs = pkgsFor system; in
        {
          default = pkgs.mkShell {
            packages = devTools pkgs;
            shellHook = ''
              export LANG=C.UTF-8
              export SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt
            '';
          };
        });
    };
}
