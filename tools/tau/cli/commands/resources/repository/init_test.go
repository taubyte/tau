package repositoryCommands

import (
	"testing"
)

func TestInitCommand_PanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("InitCommand(nil) should panic")
		}
	}()
	InitCommand(nil)
}
