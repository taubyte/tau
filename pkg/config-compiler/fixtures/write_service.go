package fixtures

import "github.com/spf13/afero"

var fileContentsGlobal_Service = `
id: QmZsZ78FJwt281gfeK3B2M1qzYx4GTpWTPv9Kx8Z2KBF2a
description: 'test_service'
tags: # optional
 - tagservice1
 - tagservice2
protocol: /testprotocol/v2
`

var fileContentsLocal_Service = `
id: QmZsZ78FJwt281gfeK3B2M1qzYx4GTpWTPv9Kx8Z2KBF2b
description: 'Test Appservice'
tags: # optional
 - tagAppservice1
 - tagAppservice2
protocol: /testprotocol/v1
`

var toWriteService = map[string]map[string]string{
	"test_service_l": {
		"application": testAppName,
		"write":       fileContentsLocal_Service,
	},
	"test_service_g": {
		"application": "",
		"write":       fileContentsGlobal_Service,
	},
}

func writeService(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "services", toWriteService)
}
