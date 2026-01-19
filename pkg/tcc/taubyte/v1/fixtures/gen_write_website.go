package fixtures

import "github.com/spf13/afero"

var fileContentsGlobal_Website = `
id: QmZmW9PQmz5Z6pYPJ6VDUPVgH7L6Xb8K1GTh8dNQzDh5gh
description: test website description
tags:
    - local
    - free
domains:
    - test_domain_g
source:
    paths:
      - /banana
    branch: main
    github:
        id: "460911436"
        fullname: tb_website_testWebsite
`

var fileContentsLocal_Website = `
id: QmZmW9PQmz5Z6pYPJ6VDUPVgH7L6Xb8K1GTh8dNQzDh5gi
description: test website description
tags:
    - local
    - free
domains:
    - test_domain_l
    - test_domain_g
source:
    paths:
      - /apple
    branch: main
    github:
        id: "460911436"
        fullname: tb_website_testWebsite
`

var toWriteWebsite = map[string]map[string]string{
	"test_website_l": {
		"application": testAppName,
		"write":       fileContentsLocal_Website,
	},
	"test_website_g": {
		"application": "",
		"write":       fileContentsGlobal_Website,
	},
}

func writeWebsite(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "websites", toWriteWebsite)
}
