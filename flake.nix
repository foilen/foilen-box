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
        d = self.lastModifiedDate;
        gitCommitDate = "${builtins.substring 0 8 d}_${builtins.substring 8 4 d}";

        # Vendor assets (see docs/build.md)
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
          outputHash = "sha256-1VzLpbqut8msTaNZVQFk0Gri1n0GbD3xq5OX1KvZUjk=";
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
          outputHash = "sha256-krraIVTRSY7laS4xFGaqVhfKK3RpzmBZV5NPTvbX0ag=";
        };
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "foilen-box";
          version = "0.0.0";

          src = ./.;

          modBuildPhase = ''
            go work vendor
          '';

          vendorHash = "sha256-E/EJpXq/J5ynXj2H7aZvcUFcmsSU5bomXeKMhEsbEoQ=";

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
            "-X" "foilen-box/internal/camera.ffmpegPath=${pkgs.ffmpeg}/bin/ffmpeg"
          ];

          nativeBuildInputs = [ pkgs.pkg-config ];
          buildInputs = [ pkgs.gtk3 pkgs.libayatana-appindicator pkgs.ffmpeg ];

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
            pkgs.ffmpeg
          ];
        };
      });
}
