package fixtures

import "github.com/spf13/afero"

var fileContentsGlobal_Database = `
id: testidglobal
description: Test Database das globe
tags: # optional
- tag1
- tag2
- glob
match: testMatchglobal
useRegex: true
access:
    network: all
replicas:
    min: 1
    max: 2
storage:
    size: 5GB
`

var fileContentsLocal_Database = `
id: testid
description: Test Database
tags: # optional
 - tag1
 - tag2
match: testMatchlocal
useRegex: true
access:
    network: all
replicas:
    min: 1
    max: 2
storage:
    size: 5GB
`

var toWriteDatabase = map[string]map[string]string{
	"test_database_l": {
		"application": testAppName,
		"write":       fileContentsLocal_Database,
	},
	"test_database_g": {
		"application": "",
		"write":       fileContentsGlobal_Database,
	},
}

func writeDatabase(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "databases", toWriteDatabase)
}
