package prompts_test

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
)

type commitMessageTest struct {
	message string
}

func (m commitMessageTest) run(t *testing.T) {
	ctx, err := mock.CLI{
		Flags: []cli.Flag{
			flags.CommitMessage,
		},
		ToSet: map[string]string{
			flags.CommitMessage.Name: m.message,
		},
	}.Run()
	if err != nil {
		t.Error(err)
		return
	}

	message := prompts.GetOrRequireACommitMessage(ctx)
	if message != m.message {
		t.Error(fmt.Errorf("expected %s, got %s", m.message, message))
	}
}

func TestCommitMessage(t *testing.T) {
	// Set to false if stuck in infinite loop or testing
	prompts.PromptEnabled = true

	commitMessageTest{
		message: "some old commit",
	}.run(t)

	commitMessageTest{
		message: "SOME CHANGE!",
	}.run(t)

	commitMessageTest{
		message: "TP-1000 BIGLY CHANGE",
	}.run(t)
}
