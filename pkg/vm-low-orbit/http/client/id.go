package client

func (f *Factory) generateClientId() uint32 {
	f.clientsLock.Lock()
	defer func() {
		f.clientsIdToGrab += 1
		f.clientsLock.Unlock()
	}()
	return f.clientsIdToGrab
}

func (c *Client) generateReqId() uint32 {
	c.reqLock.Lock()
	defer func() {
		c.reqIdToGrab += 1
		c.reqLock.Unlock()
	}()
	return c.reqIdToGrab
}
