package context

func (c *Context) SetRawResponse(val bool) {
	c.rawResponse = val
}

func (c *Context) SetVariable(key string, val interface{}) {
	c.variables[key] = val
}

func (c *Context) SetBody(new []byte) {
	c.body = new
}
