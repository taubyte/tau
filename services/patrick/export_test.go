package service

import (
	"context"
	"crypto/sha256"
	"strconv"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/kvdb"
)

// GrantRepoAccess answers the access check for tests whose token cannot write
// to the fixture repositories.
func GrantRepoAccess(token string, repoID int) {
	key := sha256.Sum256([]byte(token + "\x00" + strconv.Itoa(repoID)))
	repoAccesses.lock.Lock()
	defer repoAccesses.lock.Unlock()
	repoAccesses.entries[key] = repoAccessEntry{allowed: true, expires: time.Now().Add(time.Hour)}
}

// AssignJob records monkey as the job's monkey, as Patrick does on assignment.
func AssignJob(ctx context.Context, db kvdb.KVDB, jid string, monkey peer.ID) error {
	data, err := cbor.Marshal(Assignment{MonkeyPID: monkey.String()})
	if err != nil {
		return err
	}
	return db.Put(ctx, "/assigned/"+jid, data)
}
