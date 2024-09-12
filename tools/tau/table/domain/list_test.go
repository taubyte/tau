package domainTable_test

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	domainTable "github.com/taubyte/tau/tools/tau/table/domain"
)

func ExampleList() {
	domains := []*structureSpec.Domain{
		{
			Id:   "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
			Name: "someDomain1",
			Fqdn: "hal.computers.com",
		},
		{
			Id:   "QmbUIDhRosp5BaXDASEWSCtpkQCgQCPdRVhnxjiSHfXdC0",
			Name: "someDomain2",
			Fqdn: "hal.computers.org",
		},
	}

	domainTable.List(domains)

	// Output:
	// ┌─────────────────┬─────────────┬───────────────────┐
	// │ ID              │ NAME        │ FQDN              │
	// ├─────────────────┼─────────────┼───────────────────┤
	// │ QmbAA8...HfXdWH │ someDomain1 │ hal.computers.com │
	// ├─────────────────┼─────────────┼───────────────────┤
	// │ QmbUID...HfXdC0 │ someDomain2 │ hal.computers.org │
	// └─────────────────┴─────────────┴───────────────────┘
}
