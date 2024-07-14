package service

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	_ "github.com/taubyte/tau/services/substrate"
)

// TODO: Could shorten test doing a tns lookup or looking at patrick to see if jobs are done instead of sleep.
func TestDevRetry(t *testing.T) {
	t.Skip("Needs to be updated post code clone config for itself")
	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":     {},
			"patrick": {},
			"auth":    {},
			"monkey":  {},
			"hoarder": {},
			"substrate": {
				Others: map[string]int{"verbose": 1},
			},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS:  &commonIface.ClientConfig{},
					Auth: &commonIface.ClientConfig{},
				}.Compat(),
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	mockAuthURL, err := u.GetURLHttp(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}

	err = commonTest.RegisterTestProject(u.Context(), mockAuthURL)
	if err != nil {
		t.Error(err)
		return
	}

	err = u.RunFixture("pushCode")
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(10 * time.Second)

	err = u.RunFixture("pushConfig")
	if err != nil {
		t.Error(err)
		return
	}

	time.Sleep(60 * time.Second)
	nodePort, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		t.Error(err)
		return
	}
	url := fmt.Sprintf("http://testing_website_builder.com:%d/getsocketurl", nodePort)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Error("Failed new request error: ", err)
		return
	}
	client := &http.Client{}
	response, err := client.Do(req)
	if err != nil {
		t.Error("Failed to do client request error: ", err)
		return
	}

	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Error(err)
		return
	}

	expected := "ws-QmVp7MG674xeEhcdYGKKbtKPD2Atzgwre8AitEzvF68c64/someChannel"
	if expected != string(body) {
		t.Errorf("expected %s == %s", string(body), expected)
		return
	}

}
