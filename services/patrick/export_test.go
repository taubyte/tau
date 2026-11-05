package service

import (
	"crypto/sha256"
	"strconv"
	"time"
)

// GrantRepoAccess answers the access check for tests whose token cannot write
// to the fixture repositories.
func GrantRepoAccess(token string, repoID int) {
	key := sha256.Sum256([]byte(token + "\x00" + strconv.Itoa(repoID)))
	repoAccesses.lock.Lock()
	defer repoAccesses.lock.Unlock()
	repoAccesses.entries[key] = repoAccessEntry{allowed: true, expires: time.Now().Add(time.Hour)}
}
