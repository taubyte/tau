package flags

import (
	"regexp"
	"strings"

	"github.com/urfave/cli/v2"
)

var Paths = &cli.StringSliceFlag{
	Name:    "paths",
	Aliases: []string{"p"},
	Usage:   "HTTP paths to use for the endpoint",
}

// msysPrefix matches what Git Bash puts in front of an argument starting with
// "/": the Git install dir, e.g. "C:/Program Files/Git" for "/fn1".
var msysPrefix = regexp.MustCompile(`(?i)^[a-z]:[/\\](?:.*?[/\\])?git(?:[/\\]|$)`)

// URLPath undoes Git Bash path conversion on a value that must be a URL path.
func URLPath(s string) string {
	if loc := msysPrefix.FindStringIndex(s); loc != nil {
		return "/" + strings.ReplaceAll(s[loc[1]:], "\\", "/")
	}
	return s
}
