package fixtures

import "github.com/spf13/afero"

var fileContentsGlobal_smartOp = `
id: testsmartOpsid
description: 'Test smartOpstion'
tags: #optional
 - tagsmartOps1
 - tagsmartOps2
source: . 
execution:
 timeout: 300s
 memory: 64MB
 call: entryp
`

var fileContentsLocal_smartOp = `
id: testAppsmartOps
description: 'Test AppsmartOps'
source: libraries/test_library_l
execution:
 timeout: 300s
 memory: 64MB
 call: entrypoint6
`

var toWritesmartOp = map[string]map[string]string{
	"test_smartops_l": {
		"application": testAppName,
		"write":       fileContentsLocal_smartOp,
	},
	"test_smartops_g": {
		"application": "",
		"write":       fileContentsGlobal_smartOp,
	},
}

func writesmartOp(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "smartops", toWritesmartOp)
}
