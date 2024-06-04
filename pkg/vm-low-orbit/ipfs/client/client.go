package client

import (
	"github.com/ipfs/go-cid"
	"github.com/taubyte/go-sdk/errno"
)

func (c *Client) generateContentId() uint32 {
	c.contentLock.Lock()
	defer func() {
		c.contentIdToGrab += 1
		c.contentLock.Unlock()
	}()
	return c.contentIdToGrab
}

func (f *Factory) getClient(id uint32) (*Client, errno.Error) {
	f.clientsLock.RLock()
	client, ok := f.clients[id]
	f.clientsLock.RUnlock()
	if !ok {
		return nil, errno.ErrorClientNotFound
	}

	return client, 0
}

func (c *Client) generateContent(id uint32, cid cid.Cid, file file) *content {
	content := &content{id: id, cid: cid, file: file}

	c.contentLock.Lock()
	c.Contents[id] = content
	c.contentLock.Unlock()

	return content
}

func (c *Client) getContent(contentId uint32) (*content, errno.Error) {
	c.contentLock.RLock()
	content, ok := c.Contents[contentId]
	c.contentLock.RUnlock()
	if !ok {
		return nil, errno.ErrorContentNotFound
	}

	return content, 0
}

func (f *Factory) getClientAndContent(clientId, contentId uint32) (*Client, *content, errno.Error) {
	client, err := f.getClient(clientId)
	if err != 0 {
		return nil, nil, err
	}

	content, err := client.getContent(contentId)
	if err != 0 {
		return client, nil, err
	}

	return client, content, 0
}
