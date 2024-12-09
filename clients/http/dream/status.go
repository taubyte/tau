package http

import (
	"fmt"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
)

func (c *Client) Status() (resp dream.Status, err error) {
	err = c.get("/status", &resp)
	if err != nil {
		return
	}
	return
}

func (u *Universe) Status() (resp *dream.UniverseStatus, err error) {
	s, err := u.client.Status()
	if err != nil {
		return nil, err
	}

	if us, ok := s[u.Name]; ok {
		return &us, nil
	}

	return nil, fmt.Errorf("universe `%s` not found", u.Name)
}

func (u *Universe) Chart() (resp api.Echart, err error) {
	err = u.client.get("/les/miserables/"+u.Name, &resp)
	return
}

func (u *Universe) Id() (resp api.UniverseInfo, err error) {
	err = u.client.get("/id/"+u.Name, &resp)
	return
}
