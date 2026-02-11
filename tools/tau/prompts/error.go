package prompts

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
)

// ErrRequiredInDefaultsMode is returned when --defaults is set and a required value is missing.
var ErrRequiredInDefaultsMode = errors.New("required value missing (use command flags or run without --defaults to prompt)")

// RequiredInDefaultsModeError returns an error that identifies what is required when --defaults is set.
// The returned error wraps ErrRequiredInDefaultsMode so errors.Is(err, ErrRequiredInDefaultsMode) still works.
func RequiredInDefaultsModeError(what string) error {
	return fmt.Errorf("%s is required (use command flags or run without --defaults to prompt): %w", what, ErrRequiredInDefaultsMode)
}

func ValidateOk(err error) bool {
	if err != nil {
		printer.Out.Warning(err)
		return false
	}
	return true
}
