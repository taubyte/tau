package fixtures

import "github.com/spf13/afero"

var fileContentsGlobal_messaging = `
id: testidglobal
description: Test Messaging das globe
tags: # optional
- tag1
- tag2
- glob
local: true
channel: 
    regex: false
    match: some match
bridges:
    mqtt: 
        enable: true
    websocket:
        enable: false
`

var fileContentsLocal_messaging = `
id: testid
description: Test Messaging
tags: # optional
 - tag1
 - tag2
local: true
channel: 
    regex: false
    match: some match
bridges:
    mqtt: 
        enable: false
    websocket:
        enable: false
`

var toWriteMessaging = map[string]map[string]string{
	"test_messaging_l": {
		"application": testAppName,
		"write":       fileContentsLocal_messaging,
	},
	"test_messaging_g": {
		"application": "",
		"write":       fileContentsGlobal_messaging,
	},
}

func writeMessaging(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "messaging", toWriteMessaging)
}
