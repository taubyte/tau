package client

import (
	"fmt"
)

type devicesResponse struct {
	Ids []string `json:"devices"`
}

// Devices returns a slice of Device and an error
func (p *Project) Devices() ([]*Device, error) {
	deviceIds := &devicesResponse{}
	err := p.client.get("/project/"+p.Id+"/devices", &deviceIds)
	if err != nil {
		return nil, err
	}

	devices := make([]*Device, len(deviceIds.Ids))
	for idx, deviceId := range deviceIds.Ids {
		device, err := p.Device(deviceId)
		if err != nil {
			return nil, fmt.Errorf("getting device `%s` failed with: %s", deviceId, err)
		}

		devices[idx] = device
	}

	return devices, nil
}

// Device returns a type Device and an error
func (p *Project) Device(deviceId string) (*Device, error) {
	device := &Device{Project: p}

	err := p.client.get("/project/"+p.Id+"/device/"+deviceId, device)
	if err != nil {
		return nil, err
	}

	return device, nil
}

type updateDevicesResponse struct {
	Id string `json:"id"`
}

// Update will update a device and return an error
// Note: you must send a device with all current fields or they also will be changed
func (d *Device) Update() error {
	data := &updateDevicesResponse{}
	err := d.Project.client.put("/project/"+d.Project.Id+"/device/"+d.Id, d, data)
	if err != nil {
		return fmt.Errorf("updating device `%s` failed with: %s", d.Id, err)
	}

	if data.Id != d.Id {
		mismatchError := fmt.Sprintf("id mismatch (expected: %s, got: %s)", d.Id, data.Id)
		return fmt.Errorf("updating device `%s` failed with: %s", d.Id, mismatchError)
	}

	return nil
}
