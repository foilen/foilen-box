package webserver

// Version and CommitDate are injected via -ldflags at build time (see
// step-package-*.sh, flake.nix); left as zero defaults for plain `go build`.
var (
	Version    = "dev"
	CommitDate = ""
)

// displayVersion returns the commit date (if set at build time) and short
// hash, e.g. "20260731_1557 abc1234".
func displayVersion() string {
	if CommitDate == "" {
		return Version
	}
	return CommitDate + " " + Version
}

// appVersion is posted alongside peer announce info, e.g. "FoilenBox - 20260731_1557 abc1234".
func appVersion() string {
	return "FoilenBox - " + displayVersion()
}
