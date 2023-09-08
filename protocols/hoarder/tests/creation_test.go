package tests

import (
	"bytes"
	"reflect"
	"testing"

	commonIface "github.com/taubyte/go-interfaces/common"
	dreamland "github.com/taubyte/tau/libdream"
	_ "github.com/taubyte/tau/protocols/hoarder"
	_ "github.com/taubyte/tau/protocols/seer"

	_ "github.com/taubyte/tau/clients/p2p/hoarder"
)

func TestService(t *testing.T) {
	u := dreamland.NewUniverse(dreamland.UniverseConfig{Name: t.Name()})
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
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	simple, err := u.Simple("client")
	if err != nil {
		t.Error(err)
		return
	}

	// Pass in a file here
	var file bytes.Buffer
	file.Write([]byte("Some crap here!"))

	cid, err := u.Hoarder().Node().AddFile(&file)
	if err != nil {
		t.Error("Failed calling add file with error: ", err)
		return
	}

	// Stash first file
	resp, err := simple.Hoarder().Stash(cid) //-> should stash return this cid
	if err != nil {
		t.Error("Failed calling stash with error: ", err)
		return
	}

	_cid, err := resp.Get("cid")
	if err != nil {
		t.Error(err)
		return
	}

	__cid := _cid.(string)
	if cid != __cid {
		t.Errorf("First add/stash cid are not matching")
		return
	}

	var file2 bytes.Buffer
	file2.Write([]byte("Some crap here as well!"))

	cid, err = u.Hoarder().Node().AddFile(&file2)
	if err != nil {
		t.Error("Failed calling add second file with error: ", err)
		return
	}

	// Stash second file
	resp, err = simple.Hoarder().Stash(cid) //-> should stash return this cid
	if err != nil {
		t.Error("Failed calling stash with error: ", err)
		return
	}

	_cid, err = resp.Get("cid")
	if err != nil {
		t.Error(err)
		return
	}

	__cid = _cid.(string)

	if cid != __cid {
		t.Errorf("Second add/stash cid are not matching")
		return
	}

	// Calling stash on second file again to make sure that it doesn't stash another copy of it
	_, err = simple.Hoarder().Stash(cid)
	if err != nil {
		t.Error("Failed calling stash again on second file with error: ", err)
		return
	}

	// Should only return 2 even if calling stash 3 times
	rareCID, err := simple.Hoarder().Rare() // -> a list containing the cid
	if err != nil {
		t.Error("Failed calling rare with error: ", err)
		return
	}

	if len(rareCID) != 2 {
		t.Error("Expected 2 rare cids got ", reflect.ValueOf(rareCID).Len())
		return
	}

	cids, err := simple.Hoarder().List()
	if err != nil {
		t.Error(err)
		return
	}

	if len(cids) != 2 {
		t.Errorf("Expecting 2 cids got %d", len(cids))
		return
	}
}
