package tests

import (
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/cli/common"
	commonTest "github.com/taubyte/tau/tools/tau/common/test"
	"gotest.tools/v3/assert"
)

// TODO: find a better way that does not affect other tests
func obfuscateToken(t *testing.T) func() {
	Token = func(t *testing.T) string { return "<git-token>" }
	err := os.Setenv("TEST_GIT_TOKEN", Token(t))
	if err != nil {
		log.Fatalf("unset token failed with: %s", err)
	}

	return func() {
		Token = commonTest.GitToken
		err := os.Setenv("TEST_GIT_TOKEN", Token(t))
		if err != nil {
			log.Fatalf("reset token failed with: %s", err)
		}
	}
}

// This will generate a directory ./generate which contains all of the bash friendly commands created by the tests
func TestGenerate(t *testing.T) {
	defer obfuscateToken(t)()

	var (
		generateDir = "./test_commands"
	)

	spiders := []*testSpider{
		createApplicationMonkey(),
		createAuthMonkey(t),
		createDatabaseMonkey(),
		createDomainMonkey(),
		createFunctionMonkey(),
		createGitMonkey(t),
		createLibraryMonkey(t),
		createMessagingMonkey(),
		createProjectMonkey(),
		createServiceMonkey(),
		createSmartopsMonkey(),
		createStorageMonkey(),
		createWebsiteMonkey(t),
	}

	os.RemoveAll(generateDir)
	err := os.Mkdir(generateDir, common.DefaultDirPermission)
	assert.NilError(t, err)

	for _, spider := range spiders {
		spiderDir := path.Join(generateDir, spider.testName)
		err = os.Mkdir(spiderDir, common.DefaultDirPermission)
		assert.NilError(t, err)

		for _, monkey := range spider.tests {
			// Ignoring fail tests
			for _, child := range monkey.children {
				if child.exitCode != 0 {
					monkey.exitCode = 1
					break
				}
			}
			if monkey.exitCode != 0 {
				continue
			}

			err = writeMonkey(path.Join(spiderDir, monkey.name), monkey)
			assert.NilError(t, err)
		}
	}
}

func writeMonkey(dir string, monkey testMonkey) error {
	file, err := os.Create(strings.ReplaceAll(dir, " ", "_") + ".sh")
	if err != nil {
		return err
	}
	defer file.Close()

	err = writePreRun(file, monkey)
	if err != nil {
		return err
	}

	err = writeChildren(file, monkey)
	if err != nil {
		return err
	}

	err = writeWithHeader("# command", file, [][]string{monkey.args})
	if err != nil {
		return err
	}

	return nil
}

func cleanArgsForDocs(args []string) string {
	var newArgs string
	for idx, arg := range args {
		if idx > 0 {
			newArgs += " "
		}

		var new string
		// If it contains spaces, wrap it with ""
		if strings.Contains(arg, " ") {
			new = fmt.Sprintf(`"%s"`, arg)
		} else {
			new = arg
		}

		// Initialize the start of parsing options so we can add " \\\n\t"
		if strings.HasPrefix(new, "--") {
			new = "\\\n    " + new
		}

		newArgs += new
	}

	return "tau " + newArgs
}

func writeWithHeader(header string, file *os.File, argsSlice [][]string) error {
	fmt.Fprintln(file, header)
	for _, args := range argsSlice {
		fmt.Fprint(file, cleanArgsForDocs(args), "\n\n")
	}

	return nil
}

func writePreRun(file *os.File, monkey testMonkey) error {
	if len(monkey.preRun) == 0 {
		return nil
	}

	return writeWithHeader("# pre-run", file, monkey.preRun)
}

func writeChildren(file *os.File, monkey testMonkey) error {
	if len(monkey.children) == 0 {
		return nil
	}

	argsSlice := make([][]string, len(monkey.children))
	for idx, child := range monkey.children {
		argsSlice[idx] = child.args
	}

	return writeWithHeader("# children", file, argsSlice)
}
