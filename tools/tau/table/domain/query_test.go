package domainTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	domainTable "github.com/taubyte/tau/tools/tau/table/domain"
)

func ExampleQuery_auto() {
	domain := &structureSpec.Domain{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "this is a domain of some type",
		Tags:        []string{"apple", "orange", "banana"},
		Fqdn:        "hal.computers.com",
		CertType:    "auto",
	}

	domainTable.Query(domain)

	// Output:
	// ┌─────────────┬────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Name        │ someProject                                    │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Description │ this is a domain of some type                  │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Tags        │ apple, orange, banana                          │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ FQDN        │ hal.computers.com                              │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Cert-Type   │ auto                                           │
	// └─────────────┴────────────────────────────────────────────────┘
}

func ExampleQuery_other() {
	domain := &structureSpec.Domain{
		Id:          "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name:        "someProject",
		Description: "this is a domain of some type",
		Tags:        []string{"apple", "orange", "banana"},
		Fqdn:        "hal.computers.com",
		CertType:    "other",
		KeyFile:     "key.txt",
		CertFile:    "cert.txt",
	}

	domainTable.Query(domain)

	// Output:
	// ┌─────────────┬────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Name        │ someProject                                    │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Description │ this is a domain of some type                  │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Tags        │ apple, orange, banana                          │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ FQDN        │ hal.computers.com                              │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Cert-Type   │ other                                          │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Cert-File   │ cert.txt                                       │
	// ├─────────────┼────────────────────────────────────────────────┤
	// │ Key-File    │ key.txt                                        │
	// └─────────────┴────────────────────────────────────────────────┘
}
