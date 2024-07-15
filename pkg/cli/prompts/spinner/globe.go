package spinner

import (
	"time"

	"github.com/briandowns/spinner"
)

func Globe() (stop func()) {
	s := spinner.New(spinner.CharSets[39], 100*time.Millisecond)
	s.Start()

	return s.Stop
}
