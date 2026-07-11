package hoarder

import (
	"fmt"
	"io"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// Stash pushes a CID's bytes to a hoarder. Protocol: send the stash command,
// wait for the ready ack, frame a header (cid/target/owner/fanout), stream the
// bytes, half-close, then read the receiver's ack (which reports the imported
// CID so the caller can trust the receiver verified it).
func (c *Client) Stash(cid string, data io.Reader, opts ...hoarderIface.StashOption) error {
	cfg := &hoarderIface.StashConfig{Target: hoarderSpecs.DefaultStashReplicas, Fanout: true}
	for _, opt := range opts {
		opt(cfg)
	}

	respCh, err := c.New(
		hoarderSpecs.StashCommand,
		streamClient.Body(command.Body{}),
		streamClient.To(c.peers...),
	).Do()
	if err != nil {
		return fmt.Errorf("opening stash stream failed with: %w", err)
	}

	resp := <-respCh
	if resp == nil {
		return fmt.Errorf("no response opening stash stream for %s", cid)
	}
	if resp.Error() != nil {
		return fmt.Errorf("stash of %s rejected with: %w", cid, resp.Error())
	}
	rw := resp.ReadWriter

	header := command.New(hoarderSpecs.StashHeader, command.Body{
		hoarderSpecs.BodyCid:    cid,
		hoarderSpecs.BodyTarget: cfg.Target,
		hoarderSpecs.BodyOwner:  cfg.Owner,
		hoarderSpecs.BodyFanout: cfg.Fanout,
	})
	if err := header.Encode(rw); err != nil {
		resp.Close()
		return fmt.Errorf("sending stash header for %s failed with: %w", cid, err)
	}

	if _, err := io.Copy(rw, data); err != nil {
		resp.Close()
		return fmt.Errorf("streaming bytes for %s failed with: %w", cid, err)
	}
	resp.CloseWrite()

	ack, err := response.Decode(rw)
	if err != nil {
		return fmt.Errorf("reading stash ack for %s failed with: %w", cid, err)
	}
	if e, ok := ack["error"].(string); ok && e != "" {
		return fmt.Errorf("stash of %s rejected: %s", cid, e)
	}
	if got, _ := ack[hoarderSpecs.BodyCid].(string); got != cid {
		return fmt.Errorf("stash of %s: receiver reported cid %q", cid, got)
	}
	return nil
}
