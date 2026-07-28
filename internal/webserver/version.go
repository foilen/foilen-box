package webserver

// Version is the git commit short hash of the running build, injected via
// -ldflags at build time (see step-package-desktop.sh and
// step-package-android.sh). Left as "dev" for local builds that don't go
// through those scripts (e.g. plain `go build`/`go run`).
var Version = "dev"
