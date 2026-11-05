package codec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
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

func TestDecodeReadsARecordBack(t *testing.T) {
	raw, err := (&raftv2.Entry{Value: []byte("v"), Timestamp: 3}).MarshalVT()
	require.NoError(t, err)
	var back raftv2.Entry
	require.NoError(t, Decode(raw, &back))
	require.Equal(t, uint64(3), back.Timestamp)

	var zero raftv2.Entry
	require.NoError(t, Decode(nil, &zero), "empty bytes are the zero record")
	require.ErrorIs(t, DecodeNonEmpty(nil, &raftv2.Entry{}), ErrNotTheRecord)
	require.NoError(t, DecodeNonEmpty(raw, &raftv2.Entry{}))
}

// Another record's bytes parse as protobuf; the field this one does not declare
// is what gives them away.
func TestDecodeRefusesAFieldTheRecordDoesNotDeclare(t *testing.T) {
	raw, err := (&raftv2.ExportFSMResponse{Clock: 9}).MarshalVT()
	require.NoError(t, err)
	var keys raftv2.KeysRequest
	require.ErrorIs(t, Decode(raw, &keys), ErrNotTheRecord)

	require.Error(t, Decode([]byte{0xff}, &raftv2.Entry{}), "bytes that are not protobuf")
}

// sized answers a job whose id is as long as the request asks.
type sized struct{}

func (sized) Get(_ context.Context, req *connect.Request[plmv2.JobRef]) (*connect.Response[plmv2.Job], error) {
	n, err := strconv.Atoi(req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&plmv2.Job{Id: strings.Repeat("a", n)}), nil
}

func TestAMessageIsCompressedOnlyWhenItIsLarge(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/get", connect.NewUnaryHandler("/get", sized{}.Get, Option()))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := connect.NewClient[plmv2.JobRef, plmv2.Job](server.Client(), server.URL+"/get", Option())

	for size, compressed := range map[int]bool{100: false, CompressMinBytes * 2: true} {
		res, err := client.CallUnary(t.Context(), connect.NewRequest(&plmv2.JobRef{Id: strconv.Itoa(size)}))
		require.NoError(t, err)
		require.Len(t, res.Msg.GetId(), size)
		require.Equal(t, compressed, res.Header().Get("Content-Encoding") == "gzip", "%d bytes", size)
	}
}

func TestTheCodecIsBinaryProtoAndRefusesWhatIsNotAMessage(t *testing.T) {
	require.True(t, Codec.IsBinary())
	require.Equal(t, "proto", Codec.Name())

	_, err := Codec.Marshal("not a message")
	require.ErrorContains(t, err, "is not a proto message")
	_, err = Codec.MarshalStable("not a message")
	require.ErrorContains(t, err, "is not a proto message")
	require.ErrorContains(t, Codec.Unmarshal(nil, "not a message"), "is not a proto message")

	require.Error(t, Codec.Unmarshal([]byte{0xff}, &raftv2.ExportFSMResponse{}), "bytes that are no message, read by vtproto")
	require.Error(t, Codec.Unmarshal([]byte{0xff}, &structpb.Struct{}), "and by protobuf-go")

	stable, err := Codec.MarshalStable(sample())
	require.NoError(t, err)
	again, err := Codec.MarshalStable(sample())
	require.NoError(t, err)
	require.Equal(t, stable, again, "a map is written in one order")
	var back raftv2.ExportFSMResponse
	require.NoError(t, Codec.Unmarshal(stable, &back))
	require.True(t, proto.Equal(sample(), &back))
}
