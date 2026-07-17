package fixtures

import "github.com/spf13/afero"

var fileContentsGlobal_Streaming_Storage = `
id: QmSbe2pTyH3fpF2T8JSAk6s3js2MqUg2gi5Hx2iTWCBtqX
description: this is a storage
tags:
    - private
    - free
match: testMatchStorageStreamingGlobal
useRegex: true
access:
    network: all
streaming:
    ttl: 20s
    size: 10MB
`

var fileContentsGlobal_Object_Storage = `
id: QmVaeAmXrE4Zy94BYp3CG5UKDhmvB4gTdk72pG1oyKVbAe
description: this is a storage
tags:
    - private
    - free
match: testMatchStorageObjectGlobal
useRegex: true
access:
    network: host
object:
    versioning: false
    size: 10MB
`

var fileContentsLocal_Storage = `
id: QmSbe2pTyH3fpF2T8JSAk6s3js2MqUg2gi5Hx2iTWCBtqY
description: this is a storage
tags:
    - private
    - free
match: testMatchStorageLocal
useRegex: true
access:
    network: all
streaming:
    ttl: 20s
    size: 10MB
`

var toWriteStorage = map[string]map[string]string{
	"test_storage_l": {
		"application": testAppName,
		"write":       fileContentsLocal_Storage,
	},
	"test_storage_streaming_g": {
		"application": "",
		"write":       fileContentsGlobal_Streaming_Storage,
	},
	"test_storage_object_g": {
		"application": "",
		"write":       fileContentsGlobal_Object_Storage,
	},
}

func writeStorage(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "storages", toWriteStorage)
}
