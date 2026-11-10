package cli

import (
	"strings"
	"testing"

	"github.com/pterm/pterm"
	"gotest.tools/v3/assert"
)

// Logs and pipes get no ANSI codes; NO_COLOR turns them off on a terminal too.
func TestNoColorWhenNotATerminal(t *testing.T) {
	t.Cleanup(pterm.EnableColor)
	for _, c := range []struct {
		tty     bool
		noColor string
		color   bool
	}{{false, "", false}, {true, "1", false}, {true, "", true}} {
		pterm.EnableColor()
		setColor(c.tty, c.noColor)
		s := pterm.Success.Sprint("pushed") + pterm.Error.Sprint("failed")
		assert.Equal(t, strings.Contains(s, "\x1b["), c.color, "%+v %q", c, s)
		assert.Assert(t, strings.Contains(s, "SUCCESS"), s)
	}
}
