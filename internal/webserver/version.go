package webserver

var (
	Version    = "dev"
	CommitDate = ""
)

func displayVersion() string {
	if CommitDate == "" {
		return Version
	}
	return CommitDate + " " + Version
}

func appVersion() string {
	return "FoilenBox - " + displayVersion()
}
