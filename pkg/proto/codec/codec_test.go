package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

func sample() *raftv2.ExportFSMResponse {
	return &raftv2.ExportFSMResponse{
		Entries: map[string]*raftv2.Entry{
			"a": {Value: []byte("1"), Timestamp: 7, WallClock: 99},
			"b": {Deleted: true, Timestamp: 8},
		},
		Clock: 8,
	}
}

// What the codec writes, protobuf-go reads, and the other way round: the wire is
// the one every other protobuf peer speaks.
func TestTheCodecSpeaksTheSameWireAsProtobufGo(t *testing.T) {
	ours, err := Codec.Marshal(sample())
	require.NoError(t, err)
	var viaGo raftv2.ExportFSMResponse
	require.NoError(t, proto.Unmarshal(ours, &viaGo))
	require.True(t, proto.Equal(sample(), &viaGo))

	theirs, err := proto.Marshal(sample())
	require.NoError(t, err)
	var viaUs raftv2.ExportFSMResponse
	require.NoError(t, Codec.Unmarshal(theirs, &viaUs))
	require.True(t, proto.Equal(sample(), &viaUs))
}

// UnmarshalVT merges; the codec replaces, as proto.Unmarshal does.
func TestUnmarshalReplacesWhatWasThere(t *testing.T) {
	raw, err := Codec.Marshal(&raftv2.ExportFSMResponse{Clock: 1})
	require.NoError(t, err)

	m := sample()
	require.NoError(t, Codec.Unmarshal(raw, m))
	require.Empty(t, m.Entries)
	require.Equal(t, uint64(1), m.Clock)
}

func TestMarshalAppendKeepsWhatIsAlreadyInTheBuffer(t *testing.T) {
	out, err := Codec.MarshalAppend([]byte("prefix"), sample())
	require.NoError(t, err)
	require.Equal(t, "prefix", string(out[:6]))

	var m raftv2.ExportFSMResponse
	require.NoError(t, Codec.Unmarshal(out[6:], &m))
	require.True(t, proto.Equal(sample(), &m))
}

// A message generated without vtproto goes through protobuf-go.
func TestAMessageWithoutVTMethodsFallsBack(t *testing.T) {
	in, err := structpb.NewStruct(map[string]any{"k": "v"})
	require.NoError(t, err)

	raw, err := Codec.Marshal(in)
	require.NoError(t, err)
	var out structpb.Struct
	require.NoError(t, Codec.Unmarshal(raw, &out))
	require.True(t, proto.Equal(in, &out))

	_, err = Codec.Marshal("not a message")
	require.Error(t, err)
}

func BenchmarkMarshal(b *testing.B) {
	m := sample()
	b.Run("codec", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := Codec.Marshal(m); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("protobuf-go", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := proto.Marshal(m); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkUnmarshal(b *testing.B) {
	raw, err := proto.Marshal(sample())
	require.NoError(b, err)
	b.Run("codec", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var m raftv2.ExportFSMResponse
			if err := Codec.Unmarshal(raw, &m); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("protobuf-go", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var m raftv2.ExportFSMResponse
			if err := proto.Unmarshal(raw, &m); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// A string is whatever bytes it holds: vtproto does not check UTF-8, and nothing
// tau talks to needs it to.
func TestAStringCarriesItsBytesAsTheyAre(t *testing.T) {
	raw, err := Codec.Marshal(&raftv2.KeysRequest{Prefix: "\xff/k"})
	require.NoError(t, err)
	var back raftv2.KeysRequest
	require.NoError(t, Codec.Unmarshal(raw, &back))
	require.Equal(t, "\xff/k", back.Prefix)
}
