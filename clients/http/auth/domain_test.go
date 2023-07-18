package client

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestDomain(t *testing.T) {
	client, deferment, err := MockServerAndClient(t)
	assert.NilError(t, err)
	defer deferment()

	err = client.RegisterRepository("123456")
	assert.NilError(t, err)

	client.RegisterRepository("654321")
	p := Project{Name: "test"}
	err = p.Create(client, "123456", "654321")
	assert.NilError(t, err)

	projects, err := client.Projects()
	assert.NilError(t, err)

	project := projects[0]
	resp, err := client.RegisterDomain("taubyte.com", project.Id)
	assert.NilError(t, err)

	if len(resp.Token) == 0 {
		t.Error("Token not found")
	}
}
