package common

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/urfave/cli/v2"
)

// ErrFlagsAfterArgs is returned when a flag follows the positional name:
// urfave stops reading flags at the first argument, so the flag would be lost.
var ErrFlagsAfterArgs = errors.New("flags must come before the name, e.g. `dream new universe --bind gateway@18080/http blackhole`")

var universeNotFound = regexp.MustCompile("universe `([^`]+)` (does not exi[st]+|not found)")

// Friendly turns the raw errors users hit most into one plain line with a fix.
// universes lists the running universes; it is only called for a missing universe.
func Friendly(err error, universes func() ([]string, error)) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "no such host"), strings.Contains(msg, "i/o timeout"):
		return fmt.Errorf("Dream is not running at %s. Start it with `dream start`", DefaultDreamURL())
	case universeNotFound.MatchString(msg):
		name := universeNotFound.FindStringSubmatch(msg)[1]
		hint := fmt.Sprintf("Create it with `dream new universe %s`", name)
		if universes != nil {
			if names, lerr := universes(); lerr == nil {
				if len(names) == 0 {
					hint = "No universe is running. " + hint
				} else {
					sort.Strings(names)
					hint = "Running universes: " + strings.Join(names, ", ") + ". Pick one, or " + strings.ToLower(hint[:1]) + hint[1:]
				}
			}
		}
		return fmt.Errorf("universe `%s` not found. %s", name, hint)
	}
	return err
}

// Suggest returns the candidate closest to name by edit distance, or "" when none is close.
func Suggest(name string, candidates []string) string {
	best, bestD := "", len(name)/2+2 // farther than this is a guess, not a typo
	for _, c := range candidates {
		if d := distance(name, c); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

// distance is the Levenshtein edit distance.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// CommandNotFound prints a plain message with a "Did you mean" hint, then exits 3
// (the code urfave used before).
func CommandNotFound(c *cli.Context, name string) {
	writeNotFound(c.App.ErrWriter, c, name)
	cli.OsExiter(3)
}

func writeNotFound(w io.Writer, c *cli.Context, name string) {
	path, subs := c.App.Name, c.App.Commands
	if c.Command != nil && len(c.Command.Subcommands) > 0 {
		subs = c.Command.Subcommands
		if c.Command.HelpName != "" {
			path = c.Command.HelpName
		}
	}
	names := []string{}
	for _, s := range subs {
		if !s.Hidden {
			names = append(names, s.Names()...)
		}
	}
	fmt.Fprintf(w, "Unknown command `%s` for `%s`.", name, path)
	if s := Suggest(name, names); s != "" {
		fmt.Fprintf(w, " Did you mean `%s`?", s)
	}
	fmt.Fprintf(w, "\nRun `%s --help` to see the commands.\n", path)
}
