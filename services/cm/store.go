package cm

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	cmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2"
)

// store.go is the keys this service writes: one per issuance, carrying its
// own id, so no two writers share a key (AGENTS.md, "Designing around kvdb").

const certPrefix = "/cert/"

// kept is one issuance as stored, with its leaf parsed.
type kept struct {
	record *cmv2.Issued
	leaf   *x509.Certificate
}

func certKey(name string, notAfter time.Time, id string) string {
	return fmt.Sprintf("%s%s/%020d/%s", certPrefix, name, notAfter.Unix(), id)
}

func parseIssued(blob []byte) (*kept, error) {
	var rec cmv2.Issued
	if err := proto.Unmarshal(blob, &rec); err != nil {
		return nil, err
	}
	return parseRecord(&rec)
}

func parseRecord(rec *cmv2.Issued) (*kept, error) {
	block, _ := pem.Decode(rec.GetChain())
	if block == nil {
		return nil, errors.New("cm: an issuance holds no chain")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return &kept{record: rec, leaf: leaf}, nil
}

// newest is the issuance of name expiring last.
func (s *Service) newest(ctx context.Context, name string) (*kept, error) {
	keys, err := s.db.List(ctx, certPrefix+name+"/")
	if err != nil {
		return nil, fmt.Errorf("cm: listing %s: %w", name, err)
	}
	slices.Sort(keys)
	for i := len(keys) - 1; i >= 0; i-- {
		blob, err := s.db.Get(ctx, keys[i])
		if err != nil {
			continue
		}
		k, err := parseIssued(blob)
		if err != nil || !s.now().Before(k.leaf.NotAfter) {
			continue
		}
		return k, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotKept, name)
}

// keep stores one issuance under a key of its own.
func (s *Service) keep(ctx context.Context, rec *cmv2.Issued) error {
	name := normal(rec.GetName())
	if !s.cfg.inZone(name) {
		return fmt.Errorf("%w: %s is not in %s", ErrNotZone, name, s.cfg.Zone)
	}
	k, err := parseRecord(rec)
	if err != nil {
		return err
	}
	if err := k.leaf.VerifyHostname(strings.Replace(name, "*", "x", 1)); err != nil {
		return fmt.Errorf("cm: the chain kept for %s does not name it: %w", name, err)
	}
	ref := rec.GetKey()
	if ref.GetProject() != VaultProject || ref.GetEnvironment() != VaultEnvironment || ref.GetName() == "" {
		return errors.New("cm: an issuance's key is kept under the cloud's certificate secrets")
	}
	blob, err := proto.Marshal(&cmv2.Issued{Name: name, Chain: rec.GetChain(), Key: ref, NotAfter: k.leaf.NotAfter.Unix()})
	if err != nil {
		return err
	}
	return s.db.Put(ctx, certKey(name, k.leaf.NotAfter, ref.GetName()), blob)
}
