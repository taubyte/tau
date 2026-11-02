package raft

import (
	"sync"
	"time"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	"github.com/taubyte/tau/pkg/proto/codec"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// pool reuses messages of one type; a message is Reset before it goes back, so it
// holds nothing of the last caller's.
type pool[M any, P interface {
	*M
	proto.Message
}] struct{ p sync.Pool }

func (q *pool[M, P]) get() P {
	if m, ok := q.p.Get().(P); ok {
		return m
	}
	return P(new(M))
}

func (q *pool[M, P]) put(m P) {
	proto.Reset(m)
	q.p.Put(m)
}

var (
	entries  pool[raftv2.Entry, *raftv2.Entry]
	logs     pool[raftv2.LogEntry, *raftv2.LogEntry]
	commands pool[raftv2.Command, *raftv2.Command]
	sets     = sync.Pool{New: func() any { return new(setCommand) }}
)

// setCommand is the three messages a set is written as, reused together.
type setCommand struct {
	cmd raftv2.Command
	op  raftv2.Command_Set
	set raftv2.Set
}

// encodeSet writes a set for the log; the hot path, so its messages are reused.
func encodeSet(key string, value []byte) ([]byte, error) {
	b := sets.Get().(*setCommand)
	b.set.Key, b.set.Value = key, value
	b.op.Set = &b.set
	b.cmd.Op = &b.op
	out, err := b.cmd.MarshalVT()
	proto.Reset(&b.set)
	proto.Reset(&b.cmd)
	b.op.Set = nil
	sets.Put(b)
	return out, err
}

// encodeCommand writes a command for the log. A shape the FSM refuses is written as
// one it still refuses: no operation, or an empty batch or merge.
func encodeCommand(cmd Command) ([]byte, error) {
	if cmd.Type == CommandSet && cmd.Set != nil {
		return encodeSet(cmd.Set.Key, cmd.Set.Value)
	}
	return commandProto(cmd).MarshalVT()
}

func commandProto(cmd Command) *raftv2.Command {
	switch cmd.Type {
	case CommandSet:
		if cmd.Set != nil {
			return &raftv2.Command{Op: &raftv2.Command_Set{Set: setProto(cmd.Set)}}
		}
	case CommandDelete:
		if cmd.Delete != nil {
			return &raftv2.Command{Op: &raftv2.Command_Delete{Delete: &raftv2.Delete{Key: cmd.Delete.Key}}}
		}
	case CommandBatch:
		writes := make([]*raftv2.Write, len(cmd.Batch))
		for i, c := range cmd.Batch {
			writes[i] = &raftv2.Write{}
			switch {
			case c.Type == CommandSet && c.Set != nil:
				writes[i].Op = &raftv2.Write_Set{Set: setProto(c.Set)}
			case c.Type == CommandDelete && c.Delete != nil:
				writes[i].Op = &raftv2.Write_Delete{Delete: &raftv2.Delete{Key: c.Delete.Key}}
			}
		}
		return &raftv2.Command{Op: &raftv2.Command_Batch{Batch: &raftv2.Batch{Writes: writes}}}
	case CommandMerge:
		if cmd.Merge != nil {
			return &raftv2.Command{Op: &raftv2.Command_Merge{Merge: &raftv2.Merge{Delta: entriesProto(cmd.Merge.Delta)}}}
		}
	}
	return &raftv2.Command{}
}

func setProto(s *SetCommand) *raftv2.Set { return &raftv2.Set{Key: s.Key, Value: s.Value} }

// decodeCommand reads a log command. One that names no operation, or a batch member
// that names none, decodes to a type the FSM refuses.
func decodeCommand(raw []byte) (Command, error) {
	m := commands.get()
	defer commands.put(m)
	if err := codec.Decode(raw, m); err != nil {
		return Command{}, err
	}
	switch op := m.Op.(type) {
	case *raftv2.Command_Set:
		return Command{Type: CommandSet, Set: &SetCommand{Key: op.Set.GetKey(), Value: op.Set.GetValue()}}, nil
	case *raftv2.Command_Delete:
		return Command{Type: CommandDelete, Delete: &DeleteCommand{Key: op.Delete.GetKey()}}, nil
	case *raftv2.Command_Batch:
		writes := op.Batch.GetWrites()
		batch := make([]Command, len(writes))
		for i, w := range writes {
			switch wop := w.Op.(type) {
			case *raftv2.Write_Set:
				batch[i] = Command{Type: CommandSet, Set: &SetCommand{Key: wop.Set.GetKey(), Value: wop.Set.GetValue()}}
			case *raftv2.Write_Delete:
				batch[i] = Command{Type: CommandDelete, Delete: &DeleteCommand{Key: wop.Delete.GetKey()}}
			}
		}
		return Command{Type: CommandBatch, Batch: batch}, nil
	case *raftv2.Command_Merge:
		return Command{Type: CommandMerge, Merge: &MergeCommand{Delta: entriesFrom(op.Merge.GetDelta())}}, nil
	}
	return Command{}, nil
}

func entryProto(e CRDTEntry) *raftv2.Entry {
	return &raftv2.Entry{Value: e.Value, Timestamp: e.Timestamp, WallClock: e.WallClock, Deleted: e.Deleted}
}

func entryFrom(m *raftv2.Entry) CRDTEntry {
	return CRDTEntry{Value: m.GetValue(), Timestamp: m.GetTimestamp(), WallClock: m.GetWallClock(), Deleted: m.GetDeleted()}
}

func encodeFSMEntry(e CRDTEntry) ([]byte, error) {
	m := entries.get()
	defer entries.put(m)
	m.Value, m.Timestamp, m.WallClock, m.Deleted = e.Value, e.Timestamp, e.WallClock, e.Deleted
	return m.MarshalVT()
}

// decodeFSMEntry reads one FSM value. Every entry carries a timestamp, so empty bytes
// are not one.
func decodeFSMEntry(raw []byte) (CRDTEntry, error) {
	m := entries.get()
	defer entries.put(m)
	if err := codec.DecodeNonEmpty(raw, m); err != nil {
		return CRDTEntry{}, err
	}
	return entryFrom(m), nil
}

func entriesProto(entries map[string]CRDTEntry) map[string]*raftv2.Entry {
	out := make(map[string]*raftv2.Entry, len(entries))
	for k, e := range entries {
		out[k] = entryProto(e)
	}
	return out
}

func entriesFrom(entries map[string]*raftv2.Entry) map[string]CRDTEntry {
	out := make(map[string]CRDTEntry, len(entries))
	for k, e := range entries {
		out[k] = entryFrom(e)
	}
	return out
}

func encodeSnapshot(entries map[string]CRDTEntry, clock uint64) ([]byte, error) {
	return (&raftv2.Snapshot{Entries: entriesProto(entries), Clock: clock}).MarshalVT()
}

// decodeSnapshot reads a snapshot. An empty FSM at clock zero encodes to nothing.
func decodeSnapshot(raw []byte) (map[string]CRDTEntry, uint64, error) {
	var m raftv2.Snapshot
	if err := codec.Decode(raw, &m); err != nil {
		return nil, 0, err
	}
	return entriesFrom(m.GetEntries()), m.GetClock(), nil
}

// logProto is a log as the transport carries it; the log store writes the same
// message.
func logProto(l *raft.Log) *raftv2.LogEntry {
	var at int64
	if !l.AppendedAt.IsZero() {
		at = l.AppendedAt.UnixNano()
	}
	return &raftv2.LogEntry{Index: l.Index, Term: l.Term, Type: uint32(l.Type), Data: l.Data, Extensions: l.Extensions, AppendedAt: at}
}

func logFrom(m *raftv2.LogEntry) *raft.Log {
	l := &raft.Log{
		Index:      m.GetIndex(),
		Term:       m.GetTerm(),
		Type:       raft.LogType(m.GetType()),
		Data:       m.GetData(),
		Extensions: m.GetExtensions(),
	}
	if at := m.GetAppendedAt(); at != 0 {
		l.AppendedAt = time.Unix(0, at)
	}
	return l
}

func encodeLog(l *raft.Log) ([]byte, error) {
	var at int64
	if !l.AppendedAt.IsZero() {
		at = l.AppendedAt.UnixNano()
	}
	m := logs.get()
	defer logs.put(m)
	m.Index, m.Term, m.Type, m.Data, m.Extensions, m.AppendedAt = l.Index, l.Term, uint32(l.Type), l.Data, l.Extensions, at
	return m.MarshalVT()
}

// decodeLog reads a stored log into l. Every log has an index, so empty bytes are
// not one.
func decodeLog(raw []byte, l *raft.Log) error {
	m := logs.get()
	defer logs.put(m)
	if err := codec.DecodeNonEmpty(raw, m); err != nil {
		return err
	}
	*l = *logFrom(m)
	return nil
}

func encodeMeta(meta *snapshotMeta) ([]byte, error) {
	servers := make([]*raftv2.Server, len(meta.Configuration.Servers))
	for i, s := range meta.Configuration.Servers {
		servers[i] = &raftv2.Server{Suffrage: int32(s.Suffrage), Id: string(s.ID), Address: string(s.Address)}
	}
	return (&raftv2.SnapshotMeta{
		Id:                 meta.ID,
		Index:              meta.Index,
		Term:               meta.Term,
		Servers:            servers,
		ConfigurationIndex: meta.ConfigurationIndex,
		Size:               meta.Size,
		Version:            int32(meta.Version),
	}).MarshalVT()
}

// decodeMeta reads a snapshot's metadata. Every snapshot has an id, so empty bytes
// are not one.
func decodeMeta(raw []byte) (snapshotMeta, error) {
	var m raftv2.SnapshotMeta
	if err := codec.DecodeNonEmpty(raw, &m); err != nil {
		return snapshotMeta{}, err
	}
	var servers []raft.Server
	if len(m.GetServers()) > 0 {
		servers = make([]raft.Server, len(m.GetServers()))
		for i, s := range m.GetServers() {
			servers[i] = raft.Server{Suffrage: raft.ServerSuffrage(s.GetSuffrage()), ID: raft.ServerID(s.GetId()), Address: raft.ServerAddress(s.GetAddress())}
		}
	}
	return snapshotMeta{
		ID:                 m.GetId(),
		Index:              m.GetIndex(),
		Term:               m.GetTerm(),
		Configuration:      raft.Configuration{Servers: servers},
		ConfigurationIndex: m.GetConfigurationIndex(),
		Size:               m.GetSize(),
		Version:            raft.SnapshotVersion(m.GetVersion()),
	}, nil
}
