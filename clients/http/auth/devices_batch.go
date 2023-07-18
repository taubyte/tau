package client

import "fmt"

type batchDevicesRequest struct {
	Ids []string `json:"ids"`
}

type batchDevicesResponse struct {
	Changed []string          `json:"changed"`
	Errors  map[string]string `json:"errors,omitempty"`
}

// Parse returns the changes and errors of the batchDevicesResponse
func (resp *batchDevicesResponse) parse() (changed []string, err error) {
	if len(resp.Errors) > 0 {
		err = fmt.Errorf("errors: %v", resp.Errors)
	}

	return resp.Changed, err
}

// EnableDevices attempts to enable given deviceIds, returns the enabled devices and an error
func (p *Project) EnableDevices(deviceIds []string) (enabled []string, err error) {
	req := &batchDevicesRequest{
		Ids: deviceIds,
	}

	batchResponse := &batchDevicesResponse{}
	err = p.client.put("/project/"+p.Id+"/devices/enable", req, batchResponse)
	if err != nil {
		return nil, ErrorBatchRequest("enable", deviceIds, p.Id, err)
	}

	enabled, err = batchResponse.parse()
	if err != nil {
		return nil, ErrorBatchRequest("enable", deviceIds, p.Id, err)
	}

	return enabled, nil
}

// DisableDevices attempts to disable given deviceIds, returns the disabled devices and an error
func (p *Project) DisableDevices(deviceIds []string) (disabled []string, err error) {
	req := &batchDevicesRequest{
		Ids: deviceIds,
	}

	batchResponse := &batchDevicesResponse{}
	err = p.client.put("/project/"+p.Id+"/devices/disable", req, batchResponse)
	if err != nil {
		return nil, ErrorBatchRequest("disable", deviceIds, p.Id, err)
	}

	disabled, err = batchResponse.parse()
	if err != nil {
		return nil, ErrorBatchRequest("disable", deviceIds, p.Id, err)
	}

	return disabled, nil
}

// DeleteDevices takes a slice of deviceIds to be deleted, returns the deleted devices and an error
func (p *Project) DeleteDevices(deviceIds []string) (deleted []string, err error) {
	req := &batchDevicesRequest{
		Ids: deviceIds,
	}

	batchResponse := &batchDevicesResponse{}
	err = p.client.delete("/project/"+p.Id+"/devices", req, batchResponse)
	if err != nil {
		return nil, ErrorBatchRequest("delete", deviceIds, p.Id, err)
	}

	deleted, err = batchResponse.parse()
	if err != nil {
		return nil, ErrorBatchRequest("delete", deviceIds, p.Id, err)
	}

	return deleted, nil
}
