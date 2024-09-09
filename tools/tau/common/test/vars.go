package internal

import (
	"os"
	"testing"
)

type Repository struct {
	ID   int
	Name string
	URL  string
}

func GitToken(t *testing.T) (tkn string) {
	if tkn = os.Getenv("TEST_GIT_TOKEN"); tkn == "" {
		t.SkipNow()
	}
	return
}

var (
	GitUser     = "taubyte-test"
	Branch      = "master"
	ProjectName = "testproject"

	ConfigRepo Repository = Repository{
		ID:   485473636,
		Name: "tb_testproject",
		URL:  "https://github.com/taubyte-test/tb_testproject",
	}

	CodeRepo Repository = Repository{
		ID:   485473661,
		Name: "tb_code_testproject",
		URL:  "https://github.com/taubyte-test/tb_code_testproject",
	}
)
