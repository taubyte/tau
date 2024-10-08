package domainTable_test

import (
	"fmt"
	"testing"

	client "github.com/taubyte/tau/clients/http/auth"
	domainTable "github.com/taubyte/tau/tools/tau/table/domain"
)

var _ = ` Domain Registration                             
Entry                QmbAA8hR.hal.computers.org 
-------------------------------------------------
Type                 txt                        
-------------------------------------------------
Value                                           
												
eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiNWRzeTZYVUZlM1l1Q3l2WFR2cUF5SjJuNmlDUVNKIn0.ObvkuljZV711z0ioqLd
-------------------------------------------------`

// table render outputs line feed which cannot be represented above
var expected = []byte{32, 68, 111, 109, 97, 105, 110, 32, 82, 101, 103, 105, 115, 116, 114, 97, 116, 105, 111, 110, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 10, 32, 69, 110, 116, 114, 121, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 81, 109, 98, 65, 65, 56, 104, 82, 46, 104, 97, 108, 46, 99, 111, 109, 112, 117, 116, 101, 114, 115, 46, 111, 114, 103, 32, 10, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 10, 32, 84, 121, 112, 101, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 116, 120, 116, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 10, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 10, 32, 86, 97, 108, 117, 101, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 10, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 10, 101, 121, 74, 104, 98, 71, 99, 105, 79, 105, 74, 70, 85, 122, 73, 49, 78, 105, 73, 115, 73, 110, 82, 53, 99, 67, 73, 54, 73, 107, 112, 88, 86, 67, 74, 57, 46, 101, 121, 74, 104, 90, 71, 82, 121, 90, 88, 78, 122, 73, 106, 111, 105, 78, 87, 82, 122, 101, 84, 90, 89, 86, 85, 90, 108, 77, 49, 108, 49, 81, 51, 108, 50, 87, 70, 82, 50, 99, 85, 70, 53, 83, 106, 74, 117, 78, 109, 108, 68, 85, 86, 78, 75, 73, 110, 48, 46, 79, 98, 118, 107, 117, 108, 106, 90, 86, 55, 49, 49, 122, 48, 105, 111, 113, 76, 100, 105, 70, 51, 113, 95, 49, 99, 79, 55, 88, 86, 104, 73, 117, 87, 57, 121, 79, 105, 57, 117, 66, 118, 116, 122, 98, 108, 52, 65, 83, 56, 115, 55, 72, 74, 116, 97, 105, 67, 120, 99, 111, 53, 54, 100, 80, 74, 71, 95, 115, 79, 56, 107, 82, 110, 49, 110, 70, 79, 53, 104, 97, 116, 115, 77, 115, 103, 10, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45, 45}

func TestRegistered(t *testing.T) {
	table := domainTable.GetRegisterTable(client.DomainResponse{
		Token: "eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiNWRzeTZYVUZlM1l1Q3l2WFR2cUF5SjJuNmlDUVNKIn0.ObvkuljZV711z0ioqLdiF3q_1cO7XVhIuW9yOi9uBvtzbl4AS8s7HJtaiCxco56dPJG_sO8kRn1nFO5hatsMsg",
		Entry: "QmbAA8hR.hal.computers.org",
		Type:  "txt",
	})

	fmt.Println(table)
	if table != string(expected) {
		t.Errorf("Expected:\n%s \n\ngot:\n%s", string(expected), table)
	}
}
