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
        gitShortHash = self.shortRev or self.dirtyShortRev or "unknown";
        # self.lastModifiedDate is "YYYYMMDDHHMMSS" in UTC.
        d = self.lastModifiedDate;
        gitCommitDate = "${builtins.substring 0 8 d}_${builtins.substring 8 4 d}";

        # Mirrors browser-side vendor assets (fonts, Material Web, Mermaid)
        # to local files at build time, so the web UI never fetches them
        # from a CDN at runtime. Both are fixed-output derivations: Nix's
        # sandbox blocks network access for regular build steps, but
        # explicitly allows it here because the output is content-addressed
        # and verified against outputHash, so reproducibility is preserved
        # even though fetching requires network. Neither is committed to
        # git; both are refetched by every Nix build (content-addressed and
        # cached in the Nix store, so unchanged fetches are free after the
        # first build).
        #
        # To update either (e.g. after bumping a version in the script), set
        # its outputHash to an obviously-wrong value, run `nix build`, and
        # copy the "got:" hash Nix reports back into outputHash below.
        vendorFonts = pkgs.stdenvNoCC.mkDerivation {
          pname = "foilen-box-vendor-fonts";
          version = "0.0.0";
          dontUnpack = true;
          nativeBuildInputs = [ pkgs.curl pkgs.gnused pkgs.cacert ];
          SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
          buildPhase = ''
            bash ${./scripts/fetch-vendor-fonts.sh} $out
          '';
          dontInstall = true;
          outputHashMode = "recursive";
          outputHashAlgo = "sha256";
          outputHash = "sha256-V8dNpIgwU07qGSakd51IDsUniCn8mnGueGNz4dhsBoE=";
        };

        vendorJs = pkgs.stdenvNoCC.mkDerivation {
          pname = "foilen-box-vendor-js";
          version = "0.0.0";
          dontUnpack = true;
          nativeBuildInputs = [ pkgs.nodejs pkgs.cacert ];
          SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
          buildPhase = ''
            node ${./scripts/fetch-vendor-js.mjs} $out
          '';
          dontInstall = true;
          outputHashMode = "recursive";
          outputHashAlgo = "sha256";
          outputHash = "sha256-LTBxHE658N6FEnJyEOmQ6gvlzH7htMoXMogrPdnS3mk=";
        };
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

          vendorHash = "sha256-G7ZpbZ1OJRtvP32aSakmeM2J823LAKcZWzCnwfJKmf8=";

          # Vendor assets (fonts, JS) are fetched by the vendorFonts/vendorJs
          # FODs above and copied in here, rather than committed to git.
          postUnpack = ''
            mkdir -p $sourceRoot/internal/webserver/web/vendor-fonts
            cp -r ${vendorFonts}/. $sourceRoot/internal/webserver/web/vendor-fonts/
            chmod -R u+w $sourceRoot/internal/webserver/web/vendor-fonts

            mkdir -p $sourceRoot/internal/webserver/web/vendor-js
            cp -r ${vendorJs}/. $sourceRoot/internal/webserver/web/vendor-js/
            chmod -R u+w $sourceRoot/internal/webserver/web/vendor-js
          '';

          subPackages = [ "cmd/foilenbox" ];

          ldflags = [
            "-X" "foilen-box/internal/webserver.Version=${gitShortHash}"
            "-X" "'foilen-box/internal/webserver.CommitDate=${gitCommitDate}'"
          ];

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
