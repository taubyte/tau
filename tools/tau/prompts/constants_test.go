package prompts_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/taubyte/tau/tools/tau/prompts"
	"gotest.tools/v3/assert"
)

func TestPanicIfPromptNotEnabled(t *testing.T) {
	t.Run("prints_warning_and_panics", func(t *testing.T) {
		prompts.PromptEnabled = false
		defer func() { prompts.PromptEnabled = true }()

		var buf bytes.Buffer
		restore := printer.SetOutput(printer.WriterOutput(&buf))
		defer restore()

		func() {
			defer func() { recover() }()
			prompts.PanicIfPromptNotEnabled("branch")
		}()

		out := buf.String()
		assert.Assert(t, strings.Contains(out, "Failed to prompt"))
		assert.Assert(t, strings.Contains(out, "branch"))
	})
}
