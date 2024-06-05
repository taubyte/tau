package http

import "github.com/taubyte/tau/dream/api"

type Status map[string]UniverseStatus

type UniverseStatus struct {
	NodeCount int `json:"node-count"`
	Nodes     map[string][]string
}

func (c *Client) Status() (Status, error) {
	resp := make(Status)
	err := c.get("/status", &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (u *Universe) Status() (resp api.Echart, err error) {
	err = u.client.get("/les/miserables/"+u.Name, &resp)
	return
}

func (u *Universe) Id() (resp api.UniverseInfo, err error) {
	err = u.client.get("/id/"+u.Name, &resp)
	return
}
