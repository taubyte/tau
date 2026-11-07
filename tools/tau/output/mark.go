package output

import (
	"strings"

	"github.com/pterm/pterm"
)

// The status markers every report uses (status, doctor, push --wait): done,
// failed, needs a look, and still running.
func OK() string      { return pterm.Green("✓") }
func Failed() string  { return pterm.Red("✗") }
func Warn() string    { return pterm.Yellow("!") }
func Pending() string { return pterm.Cyan("•") }

// JobMark is the marker of a build job's status.
func JobMark(status string) string {
	switch strings.ToLower(status) {
	case "success":
		return OK()
	case "failed", "canceled", "cancelled":
		return Failed()
	}
	return Pending()
}
