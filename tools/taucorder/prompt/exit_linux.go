package prompt

import (
	"os"
	"os/exec"
)

func init() {
	exitTasks = append(exitTasks,
		func() {
			// ref: https://github.com/c-bata/go-prompt/issues/228
			rawModeOff := exec.Command("/bin/stty", "-raw", "echo")
			rawModeOff.Stdin = os.Stdin
			_ = rawModeOff.Run()
			rawModeOff.Wait()
		})

}
