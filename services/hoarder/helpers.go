package hoarder

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

// errNoConfigMatch marks the one DEFINITIVE validateConfig outcome: TNS answered
// with the config list and none matched the resource's matcher, i.e. the backing
// config was genuinely deleted or renamed. It is wrapped (fmt.Errorf %w) so that
// configDeleted can distinguish a real deletion from a TNS listing failure (a
// transient outage), which returns a distinct, unwrapped error and must never be
// read as a deletion.
var errNoConfigMatch = errors.New("no config matches")

func handleRegex(pattern, match string) error {
	matched, err := regexp.Match(pattern, []byte(match))
	if err != nil {
		return fmt.Errorf("parsing regex pattern `%s` failed with: %w", pattern, err)
	}

	if !matched {
		return fmt.Errorf("`%s` does not match regex pattern `%s`", match, pattern)
	}

	return nil
}

func checkMatch(regex bool, match, toMatch, name string) error {
	if regex {
		return handleRegex(toMatch, match)
	}

	if match != toMatch {
		return fmt.Errorf("no match %s != %s", match, toMatch)
	}
	return nil
}

// validateConfig confirms a TNS config covers the requested matcher and records
// its id on the auction carrier. Resolution is by matcher (not config id): the
// data plane only knows project/app/match/branch, so it cannot GetById. Global
// resources skip TNS validation.
func (srv *Service) validateConfig(auction *hoarderIface.Auction) error {
	if auction.MetaType == hoarderIface.Global {
		return nil
	}

	branches := spec.DefaultBranches
	if auction.Meta.Branch != "" {
		branches = []string{auction.Meta.Branch}
	}

	switch auction.MetaType {
	case hoarderIface.Database:
		configs, _, _, err := srv.tnsClient.Database().All(auction.Meta.ProjectId, auction.Meta.ApplicationId, branches...).List()
		if err != nil {
			return fmt.Errorf("listing databases for %s failed with: %w", auction.Meta.ProjectId, err)
		}
		for id, c := range configs {
			if checkMatch(c.Regex, auction.Meta.Match, c.Match, c.Name) == nil {
				auction.Meta.ConfigId = id
				return nil
			}
		}
		return fmt.Errorf("no database config matches `%s`: %w", auction.Meta.Match, errNoConfigMatch)
	case hoarderIface.Storage:
		configs, _, _, err := srv.tnsClient.Storage().All(auction.Meta.ProjectId, auction.Meta.ApplicationId, branches...).List()
		if err != nil {
			return fmt.Errorf("listing storages for %s failed with: %w", auction.Meta.ProjectId, err)
		}
		for id, c := range configs {
			if checkMatch(c.Regex, auction.Meta.Match, c.Match, c.Name) == nil {
				auction.Meta.ConfigId = id
				return nil
			}
		}
		return fmt.Errorf("no storage config matches `%s`: %w", auction.Meta.Match, errNoConfigMatch)
	}
	return fmt.Errorf("invalid resource kind %d", auction.MetaType)
}

// claimAndLoad is the placement path: validate config, write the placement
// record + this node's claim (write-on-change), open the instance kvdb, and mark
// it held. Idempotent — re-running for an already-claimed resource is a no-op.
func (srv *Service) claimAndLoad(ctx context.Context, hash string, auction *hoarderIface.Auction) error {
	if err := srv.validateConfig(auction); err != nil {
		return err
	}

	meta := &RegistryMeta{
		Kind:          auction.MetaType,
		ConfigId:      auction.Meta.ConfigId,
		ProjectId:     auction.Meta.ProjectId,
		ApplicationId: auction.Meta.ApplicationId,
		Match:         auction.Meta.Match,
		Branch:        auction.Meta.Branch,
	}
	if err := srv.putMeta(ctx, hash, meta); err != nil {
		return fmt.Errorf("writing meta for %s failed with: %w", hash, err)
	}
	if _, err := srv.load(hash); err != nil {
		return fmt.Errorf("loading %s failed with: %w", hash, err)
	}
	// Claim is written ready: the kvdb is open and the K=2 barrier delivers
	// writes synchronously, so a fresh holder can serve and back durability
	// immediately (background CRDT catch-up fills history).
	if err := srv.addClaim(ctx, hash, srv.node.ID().String()); err != nil {
		return fmt.Errorf("claiming %s failed with: %w", hash, err)
	}

	srv.markClaimed(hash)
	return nil
}

// currentHolders is the live claimant set for an instance (registry claims
// crossed with live membership).
func (srv *Service) currentHolders(ctx context.Context, hash string) ([]string, error) {
	claims, err := srv.listClaims(ctx, hash)
	if err != nil {
		return nil, err
	}
	return srv.liveClaimants(claims), nil
}
