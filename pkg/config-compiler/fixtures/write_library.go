package fixtures

import "github.com/spf13/afero"

var fileContentsGlobal_Library = `
id: QmXmed7RDZN77LV5EbUpSEtuUHvzEMyUdtGTHFMMGLOBAL
description: test library description
tags:
    - local
    - free
source:
    path: /
    branch: main
    github:
        id: "460685870"
        fullname: tb_library_testLibrary
`

var fileContentsLocal_Library = `
id: QmXmed7RDZN77LV5uuwwuutuUHvzEMyUdtGTHFMM8LOCAL
description: test library description
tags:
    - local
    - free
source:
    path: /
    branch: main
    github:
        id: "460685870"
        fullname: tb_library_testLibrary
`

var toWriteLibrary = map[string]map[string]string{
	"test_library_l": {
		"application": testAppName,
		"write":       fileContentsLocal_Library,
	},
	"test_library_g": {
		"application": "",
		"write":       fileContentsGlobal_Library,
	},
}

func writeLibrary(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "libraries", toWriteLibrary)
}
