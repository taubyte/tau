package prompts_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/taubyte/tau/tools/tau/prompts"
	"gotest.tools/v3/assert"
)

func TestValidateOk(t *testing.T) {
	t.Run("error_prints_and_returns_false", func(t *testing.T) {

		var buf bytes.Buffer
		restore := printer.SetOutput(printer.WriterOutput(&buf))
		defer restore()

		err := errors.New("validation failed")
		assert.Assert(t, !prompts.ValidateOk(err))
		assert.Assert(t, strings.Contains(buf.String(), "validation failed"))
	})
}
