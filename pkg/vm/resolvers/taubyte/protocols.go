package resolver

import (
	"fmt"

	"github.com/ipfs/go-cid"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	P_DFS             = 4242
	DFS_PROTOCOL_NAME = "dfs"

	P_FILE             = 9999
	FILE_PROTOCOL_NAME = "file"

	P_PATH             = 0x2F
	PATH_PROTOCOL_NAME = "path"
)

var ()

var internalProtocols = []ma.Protocol{
	{
		Name:       DFS_PROTOCOL_NAME,
		Code:       P_DFS,
		VCode:      ma.CodeToVarint(P_DFS),
		Size:       ma.LengthPrefixedVarSize,
		Transcoder: TranscoderCID,
	},
	{
		Name:       FILE_PROTOCOL_NAME,
		Code:       P_FILE,
		VCode:      ma.CodeToVarint(P_FILE),
		Size:       ma.LengthPrefixedVarSize,
		Path:       true,
		Transcoder: ma.TranscoderUnix,
	},
	{
		Name:       PATH_PROTOCOL_NAME,
		Code:       P_PATH,
		VCode:      ma.CodeToVarint(P_PATH),
		Size:       ma.LengthPrefixedVarSize,
		Path:       true,
		Transcoder: ma.TranscoderUnix,
	},
}

func init() {
	for _, protocol := range internalProtocols {
		if err := ma.AddProtocol(protocol); err != nil {
			panic(err)
		}
	}
}

var TranscoderCID = ma.NewTranscoderFromFunctions(cidStB, cidBtS, cidVal)

func cidStB(s string) ([]byte, error) {
	c, err := cid.Decode(s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cid addr: %s %s", s, err)
	}

	return c.Bytes(), nil
}

func cidVal(b []byte) error {
	_, err := cid.Parse(b)
	return err
}

func cidBtS(b []byte) (string, error) {
	cid, err := cid.Parse(b)
	if err != nil {
		return "", err
	}

	return cid.String(), nil
}
