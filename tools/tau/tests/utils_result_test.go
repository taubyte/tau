package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pterm/pterm"
)

func (r *commandResult) printDebugInfo() {
	if r.debug {
		// NOTE: Showing output for debugging
		pterm.FgLightYellow.Println(r.prefix, "args:")
		fmt.Print(cleanArgs(r.args), "\n\n")

		pterm.FgLightYellow.Print(r.prefix, " exitCode: ")
		fmt.Print(r.exitCode, "\n\n")

		pterm.FgLightYellow.Println(r.prefix, "Out:")
		fmt.Println(r.out1)

		pterm.FgLightYellow.Println(r.prefix, "Error:")
		fmt.Println(r.out2)

		fmt.Print(strings.Repeat("-", 50), "\n\n")
	}
}

func (r *commandResult) checkError(t *testing.T) {
	if r.err != nil {
		if r.errOut == nil || !stringContainsAll(r.out2, r.errOut) {
			r.Error(t, "failed with")
			if r.errOut != nil {
				t.Errorf("Wanted error to contain:\n%s\n", strings.Join(r.errOut, "\n"))
			}
			t.FailNow()
		}

		// Got expected error
		return
	}

	if r.errOut != nil {
		r.Error(t, "succeeded, expected to fail")
	}
}

func (r *commandResult) Error(t *testing.T, message string) {
	t.Errorf("%s: %s \nOut1 in dir `%s`:\n %s\nOut2:%s \n(%s)", r.prefix, message, r.rr.dir, r.out1, r.out2, r.name)
}

func (r *commandResult) checkWantOut(t *testing.T) {
	if !stringContainsAll(r.out1, r.wantOut) {
		t.Errorf("\ntest `%s` in dir: `%s` failed:\n\nOut1:\n%s\nWanted:\n%s\n\t", r.name, r.rr.dir, r.out1, strings.Join(r.wantOut, "\n"))
	}

	// Check reverse
	if stringContainsAny(r.out1, r.dontWantOut) {
		t.Errorf("\ntest `%s` in dir: `%s` failed:\n\nOut1:\n%s\nDid not want any of:\n%s\n\t", r.name, r.rr.dir, r.out1, strings.Join(r.dontWantOut, "\n"))
	}
}

func (r *commandResult) checkExitCode(t *testing.T) {
	if r.exitCode != r.testMonkey.exitCode {
		t.Errorf("Exit code %v != wanted %v", r.exitCode, r.testMonkey.exitCode)
		return
	}
}

func (r *commandResult) checkDirectories(t *testing.T) {
	if r.wantDir != nil {
		for _, dir := range r.wantDir {
			_path := filepath.Join(r.rr.dir, dir)
			_, err := os.Stat(_path)
			if err != nil {
				t.Errorf("error %s not found: %s", _path, err)
				return
			}
			if _, err = os.Stat(_path); os.IsNotExist(err) {
				t.Errorf("Directory %s does not exist", _path)
				return
			}
		}
	}

	if r.dontWantDir != nil {
		for _, dir := range r.dontWantDir {
			_path := filepath.Join(r.rr.dir, dir)
			if _, err := os.Stat(_path); !os.IsNotExist(err) {
				t.Errorf("Directory %s exists", _path)
				return
			}
		}
	}
}
