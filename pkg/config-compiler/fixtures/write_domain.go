package fixtures

import "github.com/spf13/afero"

var fileContentsGlobal_Domain = `
id: QmUcVJtgGZYkqFr2J9t2jV2fJJWZBvD7FJ6RyXzJY2kAj1
description: 'test_domain'
tags: # optional
 - tagdomain1
 - tagdomain2
fqdn: taubyte.global.com
certificate:
 type: inline
 key: testKey
 cert: testCert
`

var fileContentsLocal_Domain = `
id: QmZALVP7LuBpDMTyM9VGTD5uXXJhSXf7H3f8tVjZUHxAuB
description: 'Test Appdomain'
tags: # optional
 - tagAppdomain1
 - tagAppdomain2
fqdn: taubyte.local.com
certificate:
 type: inline
 key: testKeyApp
 cert: testCertApp
`

var toWriteDomain = map[string]map[string]string{
	"test_domain_l": {
		"application": testAppName,
		"write":       fileContentsLocal_Domain,
	},
	"test_domain_g": {
		"application": "",
		"write":       fileContentsGlobal_Domain,
	},
}

func writeDomain(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "domains", toWriteDomain)
}
