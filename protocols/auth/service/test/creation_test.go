package test

import (
	"fmt"
	"os"
	"testing"

	commonDreamland "bitbucket.org/taubyte/dreamland/common"
	dreamland "bitbucket.org/taubyte/dreamland/services"
	commonIface "github.com/taubyte/go-interfaces/common"
	_ "github.com/taubyte/odo/protocols/auth/service"

	commonTest "bitbucket.org/taubyte/dreamland-test/common"
	gitTest "bitbucket.org/taubyte/dreamland-test/git"
	_ "bitbucket.org/taubyte/hoarder/service"
	_ "bitbucket.org/taubyte/monkey/api/p2p"
	_ "bitbucket.org/taubyte/tns-p2p-client"
	_ "bitbucket.org/taubyte/tns/service"
	commonAuth "github.com/taubyte/odo/protocols/auth/common"
)

func TestAuth(t *testing.T) {
	t.Skip("Need to be reimplemented")
	u := dreamland.Multiverse("test-config-job")
	defer u.Stop()

	err := u.StartWithConfig(&commonDreamland.Config{
		Services: map[string]commonIface.ServiceConfig{
			"hoarder": {},
			"tns":     {},
			"auth":    {},
		},
		Simples: map[string]commonDreamland.SimpleConfig{
			"client": {
				Clients: commonDreamland.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				},
			},
		},
	})
	if err != nil {
		t.Error(err)
		return
	}

	// simple, err := u.Simple("client")
	// if err != nil {
	// 	t.Error(err)
	// 	return
	// }

	// tnsClient := simple.TNS()

	commonAuth.GetNewProjectID = func(args ...interface{}) string {
		return commonTest.ProjectID
	}

	authHttpPort, err := u.GetPortHttp(u.Auth().Node())
	if err != nil {
		t.Error(err)
		return
	}
	authHttpURL := fmt.Sprintf("http://127.0.0.1:%d", authHttpPort)

	err = commonTest.RegisterTestProject(u.Context(), authHttpURL)
	if err != nil {
		t.Error(err)
		return
	}

	gitRoot := "./testGIT"
	gitRootConfig := gitRoot + "/config"
	os.MkdirAll(gitRootConfig, 0755)
	defer os.RemoveAll(gitRootConfig)

	// clone repo
	err = gitTest.CloneToDirSSH(u.Context(), gitRootConfig, commonTest.ConfigRepo)
	if err != nil {
		t.Error(err)
		return
	}

	// read with seer
	// projectIface, err := projectLib.Open("", projectLib.SystemFS(gitRootConfig))
	// if err != nil {
	// 	t.Error(err)
	// 	return
	// }

	// err = compiler.Publish(tnsClient, projectIface, compile.IndexConfigRepo("github", fmt.Sprintf("%d", commonTest.ConfigRepo.ID)))
	// if err != nil {
	// 	t.Error(err)
	// 	return
	// }

	// // stimulate http request
	// client := CreateHttpClient()
	// req, err := http.NewRequest("GET", fmt.Sprintf("https://localhost:8883/token/domain/%s/%s", fqdn, project), nil)
	// if err != nil {
	// 	t.Error("Failed new request error: ", err)
	// 	return
	// }
	// req.Header.Add("Authorization", "github "+realToken)
	// resp, err := client.Do(req)
	// if err != nil {
	// 	t.Error("Failed to do client request error: ", err)
	// 	return
	// }
	// _data, err := ioutil.ReadAll(resp.Body)
	// fmt.Println("DATA::::::", _data)
	// if err != nil {
	// 	t.Errorf("Failed calling read all with error: %v", err)
	// }
	// var data struct {
	// 	Entry string
	// 	Token dv.Token
	// 	Type  string
	// }
	// fmt.Println("POOOOOOOOOP", string(_data))
	// err = json.Unmarshal(_data, &data)
	// fmt.Println("DATA2:::::::", data)
	// fmt.Println("TOKEN::::::", data.Token)
	// claim, err := dv.FromToken(data.Token)
	// fmt.Println("CLAIM:::::::", claim)
	// if err != nil {
	// 	t.Errorf("Failed calling from token with error: %v", err)
	// }

	// _project, err := cid.Decode(project)
	// if err != nil {
	// 	t.Errorf("Failed to decode project id with %v", err)
	// }

	// claimsToCheck, err := dv.New(dv.FQDN("qwer.com"), dv.Project(_project))
	// if err != nil {
	// 	t.Errorf("Failed calling dv new error: %v", err)
	// }
	// fmt.Println(">>>>>>>>>>>>", claimsToCheck.Address)
	// fmt.Println(">>>>>>>>>>>>CLAIM", claim.Address)
	// if claimsToCheck.Address != claim.Address {
	// 	t.Errorf("Not matching address got %s expected %s", claimsToCheck.Address, claim.Address)
	// 	return
	// }
	// expected_entry := project[0:8] + "." + fqdn
	// if data.Entry != expected_entry {
	// 	t.Errorf("Unmarshalled entry not correct expecting %s and got %s", expected_entry, data.Entry)
	// }
	// return
}
