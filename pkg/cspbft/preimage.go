package cspbft

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// The canonical encoding signatures are taken over, never the received bytes, since
// protobuf and cbor admit several encodings of one value. Every variable-length field
// is length-delimited because messages nest and no separator byte is safe. The domain
// prefix and leading Kind byte make a PREPARE useless as a COMMIT.

const (
	preimageDomain = "cspbft/preimage/v1\x00"
	recordDomain   = "cspbft/record/v1\x00"
)

// Preimage is the canonical, domain-separated, length-delimited byte string a
// Signer signs and a Verifier checks. It excludes Signature, since a signature
// cannot be an input to itself.
func Preimage(m Message) []byte {
	b := make([]byte, 0, 128)
	b = append(b, preimageDomain...)
	return appendMessage(b, m, false)
}

// appendMessage is the ONE encoder. Preimage calls it without signatures; the
// journal calls it with them. Two layouts would be two things to keep in step,
// and the one that drifted would be the one nobody signs with.
func appendMessage(b []byte, m Message, withSig bool) []byte {
	b = append(b, byte(m.Kind))
	b = appendBytes(b, []byte(m.From))
	b = binary.BigEndian.AppendUint64(b, uint64(m.View))
	b = binary.BigEndian.AppendUint64(b, uint64(m.Seq))
	b = append(b, m.Digest[:]...)
	b = appendBytes(b, []byte(m.Client))
	b = binary.BigEndian.AppendUint64(b, m.Serial)
	b = appendBytes(b, m.Proposal)
	b = appendMessages(b, m.Checkpoints, withSig)
	b = appendCertificates(b, m.Prepared, withSig)
	b = appendMessages(b, m.ViewChanges, withSig)
	b = appendMessages(b, m.PrePrepares, withSig)
	b = binary.BigEndian.AppendUint64(b, uint64(m.Epoch))
	b = append(b, m.RotationDigest[:]...)
	if withSig {
		b = appendBytes(b, m.Signature)
	}
	return b
}

func appendBytes(b, v []byte) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(len(v)))
	return append(b, v...)
}

func appendMessages(b []byte, ms []Message, withSig bool) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(len(ms)))
	for _, m := range ms {
		inner := appendMessage(nil, m, withSig)
		b = appendBytes(b, inner)
	}
	return b
}

func appendCertificates(b []byte, cs []Certificate, withSig bool) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(len(cs)))
	for _, c := range cs {
		inner := appendMessage(nil, c.PrePrepare, withSig)
		inner = appendMessages(inner, c.Prepares, withSig)
		b = appendBytes(b, inner)
	}
	return b
}

// reader is the decoding half, used only by the journal. It exists because a
// journal record must come back as a Message, and a decoder that shares its
// layout with the encoder cannot drift from it.
type reader struct {
	b []byte
	i int
}

var errShort = errors.New("truncated")

func (r *reader) u64() (uint64, error) {
	if r.i+8 > len(r.b) {
		return 0, errShort
	}
	v := binary.BigEndian.Uint64(r.b[r.i:])
	r.i += 8
	return v, nil
}

func (r *reader) digest() (Digest, error) {
	var d Digest
	if r.i+32 > len(r.b) {
		return d, errShort
	}
	copy(d[:], r.b[r.i:])
	r.i += 32
	return d, nil
}

func (r *reader) byte() (byte, error) {
	if r.i+1 > len(r.b) {
		return 0, errShort
	}
	v := r.b[r.i]
	r.i++
	return v, nil
}

func (r *reader) bytes() ([]byte, error) {
	n, err := r.u64()
	if err != nil {
		return nil, err
	}
	// The length is read from a file this process wrote, but a damaged file is
	// exactly the case this guards: without it a corrupt length allocates
	// whatever number the corruption happened to contain.
	if n > uint64(len(r.b)-r.i) {
		return nil, errShort
	}
	v := r.b[r.i : r.i+int(n)]
	r.i += int(n)
	return v, nil
}

func (r *reader) done() bool { return r.i == len(r.b) }

func readMessage(b []byte, withSig bool) (Message, error) {
	r := &reader{b: b}
	m, err := r.message(withSig)
	if err != nil {
		return Message{}, err
	}
	if !r.done() {
		return Message{}, fmt.Errorf("%d trailing bytes", len(r.b)-r.i)
	}
	return m, nil
}

func (r *reader) message(withSig bool) (Message, error) {
	var m Message
	k, err := r.byte()
	if err != nil {
		return m, err
	}
	m.Kind = Kind(k)
	from, err := r.bytes()
	if err != nil {
		return m, err
	}
	m.From = ReplicaID(from)
	v, err := r.u64()
	if err != nil {
		return m, err
	}
	m.View = View(v)
	s, err := r.u64()
	if err != nil {
		return m, err
	}
	m.Seq = Sequence(s)
	if m.Digest, err = r.digest(); err != nil {
		return m, err
	}
	client, err := r.bytes()
	if err != nil {
		return m, err
	}
	m.Client = ClientID(client)
	if m.Serial, err = r.u64(); err != nil {
		return m, err
	}
	prop, err := r.bytes()
	if err != nil {
		return m, err
	}
	if len(prop) > 0 {
		m.Proposal = append(Proposal(nil), prop...)
	}
	if m.Checkpoints, err = r.messages(withSig); err != nil {
		return m, err
	}
	if m.Prepared, err = r.certificates(withSig); err != nil {
		return m, err
	}
	if m.ViewChanges, err = r.messages(withSig); err != nil {
		return m, err
	}
	if m.PrePrepares, err = r.messages(withSig); err != nil {
		return m, err
	}
	e, err := r.u64()
	if err != nil {
		return m, err
	}
	m.Epoch = Sequence(e)
	if m.RotationDigest, err = r.digest(); err != nil {
		return m, err
	}
	if withSig {
		sig, err := r.bytes()
		if err != nil {
			return m, err
		}
		if len(sig) > 0 {
			m.Signature = append([]byte(nil), sig...)
		}
	}
	return m, nil
}

func (r *reader) messages(withSig bool) ([]Message, error) {
	n, err := r.u64()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(r.b)) {
		return nil, errShort
	}
	var out []Message
	for i := uint64(0); i < n; i++ {
		inner, err := r.bytes()
		if err != nil {
			return nil, err
		}
		m, err := readMessage(inner, withSig)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (r *reader) certificates(withSig bool) ([]Certificate, error) {
	n, err := r.u64()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(r.b)) {
		return nil, errShort
	}
	var out []Certificate
	for i := uint64(0); i < n; i++ {
		inner, err := r.bytes()
		if err != nil {
			return nil, err
		}
		ir := &reader{b: inner}
		pp, err := ir.message(withSig)
		if err != nil {
			return nil, err
		}
		prep, err := ir.messages(withSig)
		if err != nil {
			return nil, err
		}
		if !ir.done() {
			return nil, fmt.Errorf("trailing bytes in a certificate")
		}
		out = append(out, Certificate{PrePrepare: pp, Prepares: prep})
	}
	return out, nil
}
