package client

import (
	"math/rand"
	"strconv"
	"testing"

	"gotest.tools/v3/assert"
)

func TestProjects(t *testing.T) {
	client, deferment, err := MockServerAndClient(t)
	assert.NilError(t, err)
	defer deferment()

	configRepoId := "123456"
	codeRepoId := "654321"

	err = client.RegisterRepository(configRepoId)
	assert.NilError(t, err)

	err = client.RegisterRepository(codeRepoId)
	assert.NilError(t, err)

	project := &Project{Name: "test"}
	err = project.Create(client, configRepoId, codeRepoId)
	assert.NilError(t, err)

	projects, err := client.Projects()
	assert.NilError(t, err)

	assert.Equal(t, len(projects), 1)
}

func newTestProject(t *testing.T, client *Client, name string) *Project {
	project := &Project{Name: name}

	// Generate random repo ids
	configRepoId := strconv.Itoa(rand.Intn(1000000))
	codeRepoId := strconv.Itoa(rand.Intn(1000000))

	err := client.RegisterRepository(configRepoId)
	assert.NilError(t, err)

	err = client.RegisterRepository(codeRepoId)
	assert.NilError(t, err)

	err = project.Create(client, configRepoId, codeRepoId)
	assert.NilError(t, err)

	return project
}

func TestProjectList(t *testing.T) {
	client, deferment, err := MockServerAndClient(t)
	assert.NilError(t, err)
	defer deferment()

	// Create 10 projects
	for i := 0; i < 10; i++ {
		newTestProject(t, client, "test"+strconv.Itoa(i))
	}

	projects, err := client.Projects()
	assert.NilError(t, err)

	assert.Equal(t, len(projects), 10)

	for idx, project := range projects {
		assert.Equal(t, project.Name, "test"+strconv.Itoa(idx))
	}
}
