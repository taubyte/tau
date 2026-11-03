package storage

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2/storagev2connect"
)

// chunkSize is how much of a put each message carries.
const chunkSize = 256 << 10

// Client reaches one storage node. Any storage node takes any write; which one is the
// caller's routing question.
type Client struct {
	conn *rpc.Client
	svc  storagev2connect.StorageServiceClient
}

// NewClient dials one.
func NewClient(h host.Host, at libp2ppeer.ID) (*Client, error) {
	conn, err := rpc.NewClient(h, at, Protocol)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, svc: storagev2connect.NewStorageServiceClient(conn.HTTP(), conn.URL(), codec.Option())}, nil
}

func (c *Client) Close() error { c.conn.Close(); return nil }

// Put stores content as the next version of name, and returns once the cloud's
// barrier of nodes hold it.
func (c *Client) Put(ctx context.Context, at Address, name, contentType string, content io.Reader) (Version, error) {
	stream := c.svc.Put(ctx)
	head := &storagev2.PutRequest{Part: &storagev2.PutRequest_Head{Head: &storagev2.PutHead{
		At: wireAddress(at), Name: []byte(name), ContentType: contentType,
	}}}
	if err := stream.Send(head); err != nil {
		return Version{}, c.closed(stream, err)
	}
	buf := make([]byte, chunkSize)
	for {
		n, err := content.Read(buf)
		if n > 0 {
			if serr := stream.Send(&storagev2.PutRequest{Part: &storagev2.PutRequest_Chunk{Chunk: buf[:n]}}); serr != nil {
				return Version{}, c.closed(stream, serr)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			stream.CloseAndReceive()
			return Version{}, err
		}
	}
	res, err := stream.CloseAndReceive()
	if err != nil {
		return Version{}, unwire(err)
	}
	return versionFromWire(at, name, res.Msg.GetVersion())
}

// closed is the server's answer to a stream that stopped taking sends, which is why
// it stopped.
func (c *Client) closed(stream *connect.ClientStreamForClient[storagev2.PutRequest, storagev2.PutResponse], sent error) error {
	if _, err := stream.CloseAndReceive(); err != nil {
		return unwire(err)
	}
	return sent
}

// Current is the newest version of name.
func (c *Client) Current(ctx context.Context, at Address, name string) (Version, error) {
	res, err := c.svc.Current(ctx, connect.NewRequest(&storagev2.CurrentRequest{At: wireAddress(at), Name: []byte(name)}))
	if err != nil {
		return Version{}, unwire(err)
	}
	return versionFromWire(at, name, res.Msg.GetVersion())
}

// Versions is name's versions, newest first; a limit of zero is all of them.
func (c *Client) Versions(ctx context.Context, at Address, name string, limit int) ([]Version, error) {
	res, err := c.svc.Versions(ctx, connect.NewRequest(&storagev2.VersionsRequest{At: wireAddress(at), Name: []byte(name), Limit: uint32(limit)}))
	if err != nil {
		return nil, unwire(err)
	}
	out := make([]Version, 0, len(res.Msg.GetVersions()))
	for _, w := range res.Msg.GetVersions() {
		v, err := versionFromWire(at, name, w)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Delete writes a tombstone over name.
func (c *Client) Delete(ctx context.Context, at Address, name string) (Version, error) {
	res, err := c.svc.Delete(ctx, connect.NewRequest(&storagev2.DeleteRequest{At: wireAddress(at), Name: []byte(name)}))
	if err != nil {
		return Version{}, unwire(err)
	}
	return versionFromWire(at, name, res.Msg.GetVersion())
}
