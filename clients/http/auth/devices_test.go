package client

import (
	"testing"

	"gotest.tools/v3/assert"
)

func createTestProject(t *testing.T, client *Client) *Project {
	configRepoId := "123456"
	codeRepoId := "654321"

	err := client.RegisterRepository(configRepoId)
	assert.NilError(t, err)

	err = client.RegisterRepository(codeRepoId)
	assert.NilError(t, err)

	project := &Project{Name: "test"}
	err = project.Create(client, configRepoId, codeRepoId)
	assert.NilError(t, err)

	return project
}

func TestDeviceCreate(t *testing.T) {
	client, deferment, err := MockServerAndClient(t)
	assert.NilError(t, err)
	defer deferment()

	project := createTestProject(t, client)

	devices := make([]*Device, 3)
	for idx := range devices {
		devices[idx] = &Device{Project: project}
		err = client.Create(devices[idx])
		assert.NilError(t, err)
	}

	newDevices, err := project.Devices()
	assert.NilError(t, err)
	assert.Equal(t, len(newDevices), 3)
}

func createThreeTestDevices(t *testing.T, client *Client) (*Project, []*Device) {
	project := createTestProject(t, client)
	devices := make([]*Device, 3)
	for idx := range devices {
		devices[idx] = &Device{Project: project}
		err := client.Create(devices[idx])
		assert.NilError(t, err)
	}

	return project, devices
}

func TestDeviceEnable(t *testing.T) {
	client, deferment, err := MockServerAndClient(t)
	assert.NilError(t, err)
	defer deferment()

	project, devices := createThreeTestDevices(t, client)

	deviceIds := make([]string, 3)
	for idx, device := range devices {
		deviceIds[idx] = device.Id
	}

	// Disabling because devices are enabled by default
	disabled, err := project.DisableDevices(deviceIds)
	assert.NilError(t, err)
	assert.Equal(t, len(disabled), 3)

	enabled, err := project.EnableDevices([]string{devices[0].Id, devices[2].Id})
	assert.NilError(t, err)
	assert.Equal(t, len(enabled), 2)

	newDevices, err := project.Devices()
	assert.NilError(t, err)

	for _, device := range newDevices {
		if device.Id == devices[0].Id || device.Id == devices[2].Id {
			assert.Equal(t, device.Enabled, true)
		} else {
			assert.Equal(t, device.Enabled, false)
		}
	}
}

func TestDeviceDisable(t *testing.T) {
	client, deferment, err := MockServerAndClient(t)
	assert.NilError(t, err)
	defer deferment()

	project, devices := createThreeTestDevices(t, client)

	disabled, err := project.DisableDevices([]string{devices[0].Id, devices[2].Id})
	assert.NilError(t, err)
	assert.Equal(t, len(disabled), 2)

	newDevices, err := project.Devices()
	assert.NilError(t, err)

	for _, device := range newDevices {
		if device.Id == devices[0].Id || device.Id == devices[2].Id {
			assert.Equal(t, device.Enabled, false)
		} else {
			assert.Equal(t, device.Enabled, true)
		}
	}
}

func TestDeviceModify(t *testing.T) {
	client, deferment, err := MockServerAndClient(t)
	assert.NilError(t, err)
	defer deferment()

	project := createTestProject(t, client)

	testDevice := &Device{
		Project:     project,
		Description: "test device",
		Name:        "device1",
		Tags:        []string{"tag1", "tag2"},
		Type:        "type-A",
		Env: map[string]string{
			"key1": "value1",
		},
	}

	err = client.Create(testDevice)
	assert.NilError(t, err)

	gotDevice, err := project.Device(testDevice.Id)
	assert.NilError(t, err)

	assert.Equal(t, gotDevice.Description, testDevice.Description)
	assert.Equal(t, gotDevice.Name, testDevice.Name)
	assert.DeepEqual(t, gotDevice.Tags, testDevice.Tags)
	assert.Equal(t, gotDevice.Type, testDevice.Type)
	assert.DeepEqual(t, gotDevice.Env, testDevice.Env)

	testDevice.Description = "new description"
	testDevice.Name = "new_name"
	testDevice.Tags = []string{"tag3", "tag4"}
	testDevice.Type = "type-B"
	testDevice.Env = map[string]string{
		"key2": "value2",
	}

	err = testDevice.Update()
	assert.NilError(t, err)

	gotDevice, err = project.Device(testDevice.Id)
	assert.NilError(t, err)

	assert.Equal(t, gotDevice.Description, testDevice.Description)
	assert.Equal(t, gotDevice.Name, testDevice.Name)
	assert.DeepEqual(t, gotDevice.Tags, testDevice.Tags)
	assert.Equal(t, gotDevice.Type, testDevice.Type)
	assert.DeepEqual(t, gotDevice.Env, testDevice.Env)
}
