package app

// Version holds the semantic version of the tau CLI.
//
// It is set at build time by GoReleaser using:
//
//	-ldflags "-X github.com/taubyte/tau/cli/app.Version=${VERSION}"
//
// When running `go run` or building without the ldflag, it defaults to "unknown".
var (
	Version   = "unknown"
	Commit    = "unknown"
	BuildDate = "unknown"
)
