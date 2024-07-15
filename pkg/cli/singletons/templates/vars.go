package templates

import (
	"os"
	"path"

	functionSpec "github.com/taubyte/go-specs/function"
	librarySpec "github.com/taubyte/go-specs/library"
	smartOpSpec "github.com/taubyte/go-specs/smartops"
	websiteSpec "github.com/taubyte/go-specs/website"
)

var (
	TemplateRepoURL          = "https://github.com/taubyte-test/tb_templates"
	templateFolder           = path.Join(os.TempDir(), "taubyte_templates")
	templateRepositoryFolder = path.Join(templateFolder, "tb_templates")
	templateWebsiteFolder    = path.Join(templateRepositoryFolder, websiteSpec.PathVariable.String())
	templateLibraryFolder    = path.Join(templateRepositoryFolder, librarySpec.PathVariable.String())
	templateCodeFolder       = path.Join(templateRepositoryFolder, "code")
	templateFunctionsFolder  = path.Join(templateCodeFolder, functionSpec.PathVariable.String())
	templateSmartOpsFolder   = path.Join(templateCodeFolder, smartOpSpec.PathVariable.String())
)
