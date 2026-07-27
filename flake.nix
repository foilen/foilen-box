{
  description = "foilen-box: personal server/desktop application (web UI, API, and Realm P2P engine)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "foilen-box";
          version = "0.0.0";

          src = ./.;

          # This repo is a Go workspace (go.work) made of the root module and
          # ./realm. `go mod vendor` does not know about the workspace, so
          # vendor it explicitly with `go work vendor` instead.
          modBuildPhase = ''
            go work vendor
          '';
          vendorHash = "sha256-hZ87pZilbkFUeFGb8EX8kpO3u07ntxQgtui0as8cQxs=";

          subPackages = [ "cmd/foilenbox" ];

          nativeBuildInputs = [ pkgs.pkg-config ];
          buildInputs = [ pkgs.gtk3 pkgs.libayatana-appindicator ];

          postInstall = ''
            mv $out/bin/foilenbox $out/bin/foilen-box
          '';

          meta = with pkgs.lib; {
            description = "Personal server/desktop application with a web UI, API, and the Realm P2P engine";
            homepage = "https://github.com/foilen/foilen-box";
            license = licenses.mit;
          };
        };

        devShells.default = pkgs.mkShell {
          buildInputs = [
            pkgs.go
            pkgs.pkg-config
            pkgs.gtk3
            pkgs.libayatana-appindicator
          ];
        };
      });
}
