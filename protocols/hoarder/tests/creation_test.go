package tests

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	dreamland "github.com/taubyte/tau/libdream"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/seer"
	slices "github.com/taubyte/utils/slices/string"
	"gotest.tools/v3/assert"

	"github.com/taubyte/go-interfaces/services/hoarder"
	_ "github.com/taubyte/tau/clients/p2p/hoarder"
)

func TestService(t *testing.T) {
	u := dreamland.New(dreamland.UniverseConfig{Name: t.Name()})
	defer u.Stop()
	err := u.StartWithConfig(&dreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer":    {},
			"hoarder": {},
		},
		Simples: map[string]dreamland.SimpleConfig{
			"client": {
				Clients: dreamland.SimpleConfigClients{
					Seer:    &commonIface.ClientConfig{},
					Hoarder: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	assert.NilError(t, err)

	simple, err := u.Simple("client")
	assert.NilError(t, err)

	hoarderClient, err := simple.Hoarder()
	assert.NilError(t, err)

	cid1, err := addAndStashNewFile(u, hoarderClient)
	assert.NilError(t, err)

	cid2, err := addAndStashNewFile(u, hoarderClient)
	assert.NilError(t, err)

	// stash should not fail, but should only stash unique
	_, err = hoarderClient.Stash(cid2)
	assert.NilError(t, err)

	rareCids, err := hoarderClient.Rare()
	assert.NilError(t, err)
	assert.Equal(t, len(rareCids), 2)
	assert.Equal(t, slices.Contains(rareCids, cid1), true)
	assert.Equal(t, slices.Contains(rareCids, cid2), true)

	stashedCids, err := hoarderClient.List()
	assert.NilError(t, err)
	assert.Equal(t, len(stashedCids), 2)
	assert.Equal(t, slices.Contains(stashedCids, cid1), true)
	assert.Equal(t, slices.Contains(stashedCids, cid2), true)
}

func addAndStashNewFile(u *dreamland.Universe, hoarderClient hoarder.Client) (cid string, err error) {
	var file bytes.Buffer
	data := make([]byte, 8)

	if _, err = rand.Read(data); err != nil {
		return
	}

	if _, err = file.Write(data); err != nil {
		return
	}

	if cid, err = u.Hoarder().Node().AddFile(&file); err != nil {
		return
	}

	res, err := hoarderClient.Stash(cid)
	if err != nil {
		return
	}

	stashedCidIface, err := res.Get("cid")
	if err != nil {
		return
	}

	stashedCid, ok := stashedCidIface.(string)
	if !ok {
		err = fmt.Errorf("stashed cid is %T not string", stashedCidIface)
	}

	if cid != stashedCid {
		err = fmt.Errorf("cid from add:%s stashedCid:%s", cid, stashedCid)
	}

	return
}
