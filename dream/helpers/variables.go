package helpers

import (
	_ "embed"
	"fmt"
	"os"

	dreamCommon "github.com/taubyte/tau/dream"
)

var UrlPrefix = "http://" + dreamCommon.DefaultHost

//go:embed payloads/config-payload.json
var ConfigPayload []byte

//go:embed payloads/code-payload.json
var CodePayload []byte

//go:embed payloads/website-payload.json
var WebsitePayload []byte

//go:embed payloads/library-payload.json
var LibraryPayload []byte

//go:embed payloads/template-payload.json
var TemplatePayload []byte

func init() {
	var err error
	ConfigRepo.HookInfo, err = createStruct(ConfigPayload)
	if err != nil {
		panic(fmt.Sprintf("Struct config failed with %s", err.Error()))
	}

	CodeRepo.HookInfo, err = createStruct(CodePayload)
	if err != nil {
		panic(fmt.Sprintf("Struct code failed with %s", err.Error()))
	}

	WebsiteRepo.HookInfo, err = createStruct(WebsitePayload)
	if err != nil {
		panic(fmt.Sprintf("Struct website failed with %s", err.Error()))
	}

	LibraryRepo.HookInfo, err = createStruct(LibraryPayload)
	if err != nil {
		panic(fmt.Sprintf("Struct library failed with %s", err.Error()))
	}
}

var (
	TestFQDN    = "hal.computers.com"
	GitToken    = os.Getenv("TEST_GIT_TOKEN")
	GitProvider = "github"
	ProjectID   = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	GitUser     = "taubyte-test"
	ProjectName = "testproject"
	KeepRepos   = []string{"tb_testproject", "tb_code_testproject", "tb_website_testwebsite", "tb_library_testLibrary", "tb_website_reactdemo", "tb_website_socketWebsite"}

	ConfigRepo Repository = Repository{
		ID:     485473636,
		HookId: 357884401,
		Name:   "tb_testproject",
		URL:    "https://github.com/taubyte/tb_test_project/", //https://github.com/taubyte-test/tb_testproject",
	}

	CodeRepo Repository = Repository{
		ID:     485473661,
		HookId: 357884405,
		Name:   "tb_code_testproject",
		URL:    "https://github.com/taubyte-test/tb_code_testproject",
	}

	WebsiteRepo Repository = Repository{
		ID:     485476045,
		HookId: 357896781,
		Name:   "tb_website_testwebsite",
		URL:    "https://github.com/taubyte-test/tb_website_testwebsite",
	}

	LibraryRepo Repository = Repository{
		ID:     495584539,
		HookId: 359684755,
		Name:   "tb_library_testLibrary",
		URL:    "https://github.com/taubyte-test/tb_library_testLibrary",
	}
)
