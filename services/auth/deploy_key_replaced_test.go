package auth

import (
	"context"
	"testing"

	"github.com/taubyte/tau/core/kvdb"
	"golang.org/x/crypto/ssh"
	"gotest.tools/v3/assert"
)

// A key found by its value: the same title may be another cloud's key.
func TestRemoveDeployKeyOnlyRemovesThatKey(t *testing.T) {
	k := &keyServer{keys: map[int64][2]string{
		1: {deployKeyName, "ecdsa-sha2-nistp256 ANOTHER_CLOUD"},
		2: {deployKeyName, "ecdsa-sha2-nistp256 REPLACED"},
		3: {deployKeyName + "_cloud.example", "ecdsa-sha2-nistp256 NEW"},
	}}
	client := keyClient(t, k)

	if err := client.RemoveDeployKey("ecdsa-sha2-nistp256 REPLACED comment\n"); err != nil {
		t.Fatal(err)
	}
	if len(k.deleted) != 1 || k.deleted[0] != 2 {
		t.Fatalf("deleted %v", k.deleted)
	}

	if err := client.RemoveDeployKey("ecdsa-sha2-nistp256 NOT_ON_GITHUB"); err != nil {
		t.Fatal(err)
	}
	if len(k.deleted) != 1 {
		t.Fatalf("deleted %v", k.deleted)
	}
}

type replacedKeyClient struct {
	*mockGitHubClient
	onRemove func(key string)
}

func (c *replacedKeyClient) RemoveDeployKey(key string) error {
	c.onRemove(key)
	return nil
}

type noBatchKV struct{ kvdb.KVDB }

func (noBatchKV) Batch(context.Context) (kvdb.Batch, error) { return nil, context.Canceled }

// Upgrading: the key a repository holds from an older release, whatever its
// title, goes once its replacement is stored.
func TestRegisterRemovesTheKeyItReplaced(t *testing.T) {
	ctx := context.Background()
	svc, err := New(ctx, newTestConfig(t, 12392))
	assert.NilError(t, err)
	defer svc.Close()
	svc.tnsClient = &mockTNSClient{}

	const repoID = "4343"
	keyPath := "/repositories/github/" + repoID + "/key"
	public := func(private []byte) string {
		signer, err := ssh.ParsePrivateKey(private)
		assert.NilError(t, err)
		return sameDeployKey(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	}

	var removed []string
	client := &replacedKeyClient{mockGitHubClient: &mockGitHubClient{}, onRemove: func(key string) {
		removed = append(removed, sameDeployKey(key))
	}}

	_, err = svc.registerGitHubRepository(ctx, client, repoID)
	assert.NilError(t, err)
	assert.Equal(t, len(removed), 0, "a first registration replaced nothing")

	old, err := svc.KV().Get(ctx, keyPath)
	assert.NilError(t, err)

	client.onRemove = func(key string) {
		stored, err := svc.KV().Get(ctx, keyPath)
		assert.NilError(t, err)
		assert.Assert(t, public(stored) != sameDeployKey(key), "removed the key now stored")
		removed = append(removed, sameDeployKey(key))
	}
	_, err = svc.registerGitHubRepository(ctx, client, repoID)
	assert.NilError(t, err)
	assert.DeepEqual(t, removed, []string{public(old)})

	removed = nil
	svc.db = noBatchKV{svc.db}
	_, err = svc.registerGitHubRepository(ctx, client, repoID)
	assert.Assert(t, err != nil)
	assert.Equal(t, len(removed), 0, "removed the old key although its replacement was not stored")
}
