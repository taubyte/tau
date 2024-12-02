package http

type UniverseInfo struct {
	SwarmKey  []byte `json:"swarm-key"`
	NodeCount int    `json:"node-count"`
}

type MultiverseInfo map[string]UniverseInfo

func (c *Client) Universes() (MultiverseInfo, error) {
	resp := make(MultiverseInfo)
	err := c.get("/universes", &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
