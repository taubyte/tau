package fixtures

import "github.com/spf13/afero"

var fileContentsGlobalHTTP_Function = `
id: testhttp
description: some description
tags:
    - some
    - tag
    - smartops:test_smartops_g
source: libraries/test_library_g
execution:
    timeout: 10s
    memory: 10GB
    call: entry
trigger:
    type: http
    method: post
    paths:
      - /example
`

var fileContentsGlobalP2P_Function = `
id: testp2p
description: p2p description
tags:
    - p2p
    - test
source: libraries/test_library_g
execution:
    timeout: 10s
    memory: 10KB
    call: entry
trigger:
    type: p2p
    command: testCommand
    local: false
    service: /test/v1
`

var fileContentsGlobalPUB_SUB_Function = `
id: QmdZsK4VyNdUUs1EQZS33iR6UzFKwffDpwiFdNGVupmFe6
description: pubsub description
tags:
    - pubsub
    - test
source: .
execution:
    timeout: 10m
    memory: 10MB
    call: testEntry
domains:
    - test_domain_g
trigger:
    type: pubsub
    channel: testChannel
    local: true
`

var fileContentsLocal_Function = `
id: testlocalID
description: some local description
tags:
    - some
    - tag
    - smartops:test_smartops_l
source: libraries/test_library_l
domains:
    - test_domain_l
    - test_domain_g
execution:
    timeout: 10s
    memory: 10GB
    call: entry
trigger:
    type: http
    method: post
    paths:
      - /example
`

var toWriteFunction = map[string]map[string]string{
	"test_function_l": {
		"application": testAppName,
		"write":       fileContentsLocal_Function,
	},
	"test_function_ghttp": {
		"application": "",
		"write":       fileContentsGlobalHTTP_Function,
	},
	"test_function_gp2p": {
		"application": "",
		"write":       fileContentsGlobalP2P_Function,
	},
	"test_function_gpubsub": {
		"application": "",
		"write":       fileContentsGlobalPUB_SUB_Function,
	},
}

func writeFunction(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "functions", toWriteFunction)
}
