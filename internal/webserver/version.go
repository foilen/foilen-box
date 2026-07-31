package webserver

// Version is the git commit short hash of the running build, and CommitDate
// is that commit's date/time (UTC). Both are injected via -ldflags at build
// time (see step-package-desktop.sh, step-package-android.sh, and
// flake.nix). Left as their zero defaults for local builds that don't go
// through those (e.g. plain `go build`/`go run`).
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
