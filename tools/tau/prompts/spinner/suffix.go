package spinner

import (
	"sync"
	"time"

	"github.com/briandowns/spinner"
)

// StartWithSuffix starts a spinner with an initial suffix and returns:
// - updateSuffix: call to change the message (e.g. " Step: build...")
// - stop: call when done to stop the spinner
func StartWithSuffix(suffix string) (updateSuffix func(string), stop func()) {
	s := spinner.New(spinner.CharSets[39], 100*time.Millisecond)
	s.Suffix = suffix
	s.Start()

	var mu sync.Mutex
	stopped := false

	updateSuffix = func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		if !stopped {
			s.Suffix = msg
		}
	}

	stop = func() {
		mu.Lock()
		defer mu.Unlock()
		if !stopped {
			stopped = true
			s.Stop()
		}
	}

	return updateSuffix, stop
}
