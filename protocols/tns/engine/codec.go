package engine

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/multiformats/go-multicodec"
	"github.com/multiformats/go-varint"
)

func codecBytes() []byte {
	return varint.ToUvarint(uint64(Codec))
}

func getCodec(data []byte) (multicodec.Code, int, error) {
	code, shift, err := varint.FromUvarint(data)
	if err != nil {
		return 0, -1, err
	}
	for _, kc := range multicodec.KnownCodes() {
		if uint64(kc) == code {
			return kc, shift, nil
		}
	}
	return 0, -1, fmt.Errorf("Unknown codec %x", code)
}

func encode(data interface{}) ([]byte, error) {
	_data, err := cbor.Marshal(data)
	if err != nil {
		return nil, err
	}
	_data = append(codecBytes(), _data...)
	return _data, nil
}

func decode(raw []byte, data interface{}) error {
	_code, _shift, err := getCodec(raw)
	if err != nil {
		return err
	}
	if _code != multicodec.Cbor {
		return fmt.Errorf("codec `%s` not supported", _code.String())
	}
	raw = raw[_shift:]
	err = cbor.Unmarshal(raw, data)
	if err != nil {
		return err
	}
	return nil
}
