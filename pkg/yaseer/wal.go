// Op-based write-ahead log.
//
// Problem: a kill / power loss between Commit() and the next Sync()
// drops the committed work — it lived only in the in-memory document
// cache. We want commits to be durable independent of when the
// caller chooses to flush.
//
// Solution: each Commit() appends a frame describing the ops it
// just applied (Get / CreateDocument / Set / etc.) to a WAL file
// and fsyncs. Sync() flushes the document cache to disk and then
// truncates the WAL. On the next New(), any frames sitting in the
// WAL are replayed by re-building a Query from the recorded ops and
// calling Commit()+Sync() against the freshly-loaded documents.
//
// Why ops instead of doc bytes:
//
//   1. Tiny payload. A 100 KB config.yaml with one Set is one
//      "Set foo.bar = val" frame, not 100 KB of YAML.
//
//   2. External edits compose. If a human edits config.yaml
//      between crash and restart, replay reads the user's edit,
//      applies our Set on top, writes both — the human edit is
//      preserved unless the same key is touched (last writer wins,
//      which is the same outcome they'd see in any concurrent
//      editor).
//
// Frame layout (one per Commit, appended):
//
//	frameMagic[4]    "YOP1"
//	frameLen[4 BE]   -- bytes from opCount to last opVal, INCLUSIVE
//	opCount[2 BE]
//	for each op:
//	  opType[1]      -- matches opType* constants in types.go
//	  nameLen[2 BE]
//	  name[nameLen]
//	  valLen[4 BE]   -- 0 when op carries no value
//	  val[valLen]    -- yaml.Marshal'd value bytes
//	crc32[4 BE]      -- IEEE of (frameLen .. last opVal)
//
// A partially-written trailing frame fails the crc check and is
// dropped (everything before it is replayed; everything from the
// bad frame onwards is discarded). That's the durability seam:
// once the frame's crc lands, the commit is recoverable.

package seer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"reflect"

	"gopkg.in/yaml.v3"
)

const (
	frameMagic = "YOP1"
)

// Wire codes for op handlers. The on-disk opType + handler pair
// doesn't survive a function-pointer round trip, so the WAL stores
// these compact codes instead and rehydrates the right handler at
// load time.
const (
	wireOpGet            byte = 0
	wireOpGetOrCreate    byte = 1
	wireOpCreateDocument byte = 2
	wireOpSet            byte = 3
	wireOpDelete         byte = 4
)

// opToWire identifies which wire code best represents an op. Falls
// back by handler pointer because Delete and Set both use
// opTypeSet — only the handler distinguishes them.
func opToWire(o op) (byte, error) {
	pc := reflect.ValueOf(o.handler).Pointer()
	switch pc {
	case reflect.ValueOf(opGetOrCreate).Pointer():
		if o.opType == opTypeGet {
			return wireOpGet, nil
		}
		return wireOpGetOrCreate, nil
	case reflect.ValueOf(opCreateDocument).Pointer():
		return wireOpCreateDocument, nil
	case reflect.ValueOf(opSetInYaml).Pointer():
		return wireOpSet, nil
	case reflect.ValueOf(opDelete).Pointer():
		return wireOpDelete, nil
	}
	return 0, fmt.Errorf("unknown op handler at %x", pc)
}

// wireToOp rebuilds an op from its wire code + serialised name/value
// during replay.
func wireToOp(w byte, name string, value any) (op, error) {
	switch w {
	case wireOpGet:
		return op{opType: opTypeGet, name: name, handler: opGetOrCreate}, nil
	case wireOpGetOrCreate:
		return op{opType: opTypeGetOrCreate, name: name, handler: opGetOrCreate}, nil
	case wireOpCreateDocument:
		return op{opType: opTypeCreateDocument, name: name, handler: opCreateDocument}, nil
	case wireOpSet:
		return op{opType: opTypeSet, value: value, handler: opSetInYaml}, nil
	case wireOpDelete:
		return op{opType: opTypeSet, handler: opDelete}, nil
	}
	return op{}, fmt.Errorf("unknown op wire code %d", w)
}

// appendCommitWAL writes a single frame describing `ops` to the WAL
// and fsyncs. No-op when WAL is disabled (s.walPath == "").
// Called by Query.Commit() after the in-memory mutation succeeds.
func (s *Seer) appendCommitWAL(ops []op) error {
	if s.walPath == "" {
		return nil
	}
	body, err := encodeOpsFrame(ops)
	if err != nil {
		return fmt.Errorf("wal: encode frame: %w", err)
	}

	// bytes.Buffer.Write never fails; assembly is in-memory only.
	var buf bytes.Buffer
	buf.WriteString(frameMagic)
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(body)))
	buf.Write(body)
	sum := crc32.ChecksumIEEE(buf.Bytes()[4:]) // frameLen..end-of-body
	_ = binary.Write(&buf, binary.BigEndian, sum)

	f, err := s.fs.OpenFile(s.walPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("wal: open %s: %w", s.walPath, err)
	}
	defer f.Close()
	if _, err := f.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("wal: append %s: %w", s.walPath, err)
	}
	if syncer, ok := f.(interface{ Sync() error }); ok {
		if err := syncer.Sync(); err != nil {
			return fmt.Errorf("wal: fsync %s: %w", s.walPath, err)
		}
	}
	return nil
}

// encodeOpsFrame serialises one Commit's ops list into the WAL frame
// body (everything between frameLen and crc32 in the on-disk layout).
func encodeOpsFrame(ops []op) ([]byte, error) {
	// All writes target bytes.Buffer, whose Write contract is "never
	// returns a non-nil error" (see pkg docs). The only places this
	// function can fail are opToWire (unknown handler), explicit
	// length checks, and yaml.Marshal — handled below.
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(ops)))
	for _, o := range ops {
		wire, err := opToWire(o)
		if err != nil {
			return nil, err
		}
		buf.WriteByte(wire)
		if len(o.name) > 0xFFFF {
			return nil, fmt.Errorf("op name too long (%d bytes)", len(o.name))
		}
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(o.name)))
		buf.WriteString(o.name)
		var val []byte
		if o.value != nil {
			marshalled, err := yaml.Marshal(o.value)
			if err != nil {
				return nil, fmt.Errorf("marshal op value: %w", err)
			}
			val = marshalled
		}
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(val)))
		buf.Write(val)
	}
	return buf.Bytes(), nil
}

// loadCommitFrames reads the WAL and returns every COMPLETE frame in
// order. A trailing partial / corrupted frame is dropped silently —
// it represents a crash mid-append, before that commit became
// durable, so it's correct to discard.
//
// Returns (nil, nil) when WAL is absent / empty.
func (s *Seer) loadCommitFrames() ([][]op, error) {
	if s.walPath == "" {
		return nil, nil
	}
	f, err := s.fs.Open(s.walPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("wal: open %s: %w", s.walPath, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("wal: read %s: %w", s.walPath, err)
	}

	var out [][]op
	for len(data) > 0 {
		if len(data) < 4 {
			break
		}
		if string(data[:4]) != frameMagic {
			break
		}
		if len(data) < 8 {
			break
		}
		bodyLen := binary.BigEndian.Uint32(data[4:8])
		// Whole frame = magic(4) + bodyLen(4) + body + crc(4).
		totalLen := 4 + 4 + int(bodyLen) + 4
		if len(data) < totalLen {
			break // partial trailing frame; drop
		}
		// CRC is over [frameLen..end-of-body].
		want := binary.BigEndian.Uint32(data[totalLen-4 : totalLen])
		got := crc32.ChecksumIEEE(data[4 : totalLen-4])
		if want != got {
			slog.Warn("yaseer: WAL frame failed crc; truncating recovery here",
				"path", s.walPath, "offset", len(out))
			break
		}
		ops, err := decodeOpsFrame(data[8 : totalLen-4])
		if err != nil {
			slog.Warn("yaseer: WAL frame failed decode; truncating recovery here",
				"path", s.walPath, "error", err)
			break
		}
		out = append(out, ops)
		data = data[totalLen:]
	}
	return out, nil
}

// decodeOpsFrame reverses encodeOpsFrame.
func decodeOpsFrame(body []byte) ([]op, error) {
	if len(body) < 2 {
		return nil, errors.New("frame too short for opCount")
	}
	n := binary.BigEndian.Uint16(body[:2])
	body = body[2:]
	ops := make([]op, 0, n)
	for i := uint16(0); i < n; i++ {
		if len(body) < 1 {
			return nil, errors.New("truncated opType")
		}
		ot := int(body[0])
		body = body[1:]
		if len(body) < 2 {
			return nil, errors.New("truncated nameLen")
		}
		nameLen := binary.BigEndian.Uint16(body[:2])
		body = body[2:]
		if len(body) < int(nameLen) {
			return nil, errors.New("truncated name")
		}
		name := string(body[:nameLen])
		body = body[nameLen:]
		if len(body) < 4 {
			return nil, errors.New("truncated valLen")
		}
		valLen := binary.BigEndian.Uint32(body[:4])
		body = body[4:]
		if len(body) < int(valLen) {
			return nil, errors.New("truncated val")
		}
		var val any
		if valLen > 0 {
			if err := yaml.Unmarshal(body[:valLen], &val); err != nil {
				return nil, fmt.Errorf("unmarshal op value: %w", err)
			}
		}
		body = body[valLen:]
		rebuilt, err := wireToOp(byte(ot), name, val)
		if err != nil {
			return nil, err
		}
		ops = append(ops, rebuilt)
	}
	if len(body) != 0 {
		return nil, errors.New("trailing bytes in frame")
	}
	return ops, nil
}

// replayWAL reads pending frames, applies each as a fresh Commit
// against the freshly-loaded document cache, then Sync()s. Truncates
// the WAL after a successful Sync. A frame whose Commit fails is
// logged and skipped; remaining frames still get a chance.
//
// Called from New() before the caller sees the Seer instance.
func (s *Seer) replayWAL() error {
	if s.walPath == "" {
		return nil
	}
	frames, err := s.loadCommitFrames()
	if err != nil {
		return err
	}
	if len(frames) == 0 {
		// Empty/absent/corrupt WAL — nothing to replay. Don't clear
		// it; the next Sync will overwrite or remove as needed.
		return nil
	}
	for i, ops := range frames {
		q := queryFromOps(s, ops)
		if err := q.Commit(); err != nil {
			slog.Warn("yaseer: WAL replay frame failed", "index", i, "error", err)
			continue
		}
	}
	// Flush the recovered state and clear the WAL together — the
	// inner Sync handles the FS write, then we truncate the log.
	if err := s.syncLocked(); err != nil {
		return fmt.Errorf("wal: sync after replay: %w", err)
	}
	return s.clearWAL()
}

// clearWAL removes the WAL file. ENOENT is ignored.
func (s *Seer) clearWAL() error {
	if s.walPath == "" {
		return nil
	}
	if err := s.fs.Remove(s.walPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("wal: clear %s: %w", s.walPath, err)
	}
	return nil
}
