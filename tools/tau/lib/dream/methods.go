package dreamLib

import (
	"os"
	"os/exec"
	"strings"

	"github.com/taubyte/tau/tools/tau/constants"
)

func IsRunning() bool {
	out, err := ExecuteWithCapture("status", "id")
	if err != nil {
		return false
	}

	return strings.Contains(out, "SUCCESS")
}

func dream(args ...string) (*exec.Cmd, error) {
	binaryLoc := os.Getenv(constants.DreamBinaryLocationEnvVarName)
	if len(binaryLoc) < 1 {
		// Attempts to run command that is on path
		binaryLoc = "dream"
	}

	// TODO confirm binary
	// dreamI18n.Help().IsAValidBinary()

	return exec.Command(binaryLoc, args...), nil
}

func Execute(args ...string) error {
	cmd, err := dream(args...)
	if err != nil {
		return err
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err

	}

	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}

func ExecuteWithCapture(args ...string) (string, error) {
	cmd, err := dream(args...)
	if err != nil {
		return "", err
	}

	outBytes, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(outBytes), nil
}
