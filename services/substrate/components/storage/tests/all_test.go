package tests

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/assert"

	gitTest "github.com/taubyte/tau/dream/helpers/git"

	_ "github.com/taubyte/tau/clients/p2p/tns"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/pkg/kvdb"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	_ "github.com/taubyte/tau/services/substrate"
	storages "github.com/taubyte/tau/services/substrate/components/storage"
	_ "github.com/taubyte/tau/services/tns"

	_ "embed"
)

//go:embed assets/test.webm
var sampleVideo []byte

//go:embed assets/test2.webm
var sampleVideo2 []byte

const (
	projectString = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	fakeFileName  = "TestingFile"
	storageMatch1 = "testStorage1"
	storageMatch2 = "/test/regex"
	storageMatch3 = "testStorage3"
	fileData      = "To whom it may concern, Hello!"
	video1Cid     = "bafybeifxxbixu7vjyvxvp3sxsm2jvndkv5ucenubzcjizsitq6zl5bhh54"
	video3Cid     = "bafybeihwbkdwcsvpoixhxzopyop6lx3svnxayro3x2vr3zycle2ygvp3om"

	expectedCommitId = "testCommit2"
)

// TODO: FIX TESTS

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

func TestAll(t *testing.T) {
	t.Skip("this is a broken project")
	meta := patrick.Meta{}
	meta.Repository.ID = 1234567890
	meta.Repository.Branch = "master"
	meta.HeadCommit.ID = "commitID"
	meta.Repository.Provider = "github"

	u := dream.New(dream.UniverseConfig{Name: t.Name()})
	defer u.Stop()

	err := u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":       {},
			"substrate": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {
				Clients: dream.SimpleConfigClients{
					TNS: &commonIface.ClientConfig{},
				}.Compat(),
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

	tnsClient, err := simple.TNS()
	assert.NilError(t, err)

	dbFactory := kvdb.New(u.Substrate().Node())
	service, err := storages.New(u.Substrate(), dbFactory)
	if err != nil {
		t.Errorf("Creating storages service failed with: %s", err.Error())
		return
	}

	testBuf := new(bytes.Buffer)
	_, err = testBuf.Write([]byte(fileData))
	if err != nil {
		t.Error(err)
		return
	}

	_cid, err := service.Add(testBuf)
	if err != nil {
		t.Errorf("Adding file to ipfs failed with: %s", err.Error())
		return
	}

	file, err := service.GetFile(u.Context(), _cid)
	if err != nil {
		t.Errorf("Getting file %s from ipfs failed with: %s", _cid, err.Error())
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		t.Error(err)
		return
	}

	if string(data) != fileData {
		t.Errorf("Did not get same data %s != %s", string(data), fileData)
		return
	}

	gitRoot := "./testGIT"
	defer os.RemoveAll(gitRoot)
	gitRootConfig := gitRoot + "/config"
	err = os.MkdirAll(gitRootConfig, 0755)
	assert.NilError(t, err)

	if err = gitTest.CloneToDirSSH(u.Context(), gitRootConfig, commonTest.ConfigRepo); err != nil {
		t.Error(err)
		return
	}

	projectIface, err := projectLib.Open(projectLib.SystemFS(gitRootConfig))
	if err != nil {
		t.Error(err)
		return
	}

	rc, err := compile.CompilerConfig(projectIface, meta, generatedDomainRegExp)
	if err != nil {
		t.Error(err)
		return
	}

	compiler, err := compile.New(rc, compile.Dev())
	if err != nil {
		t.Error(err)
		return
	}

	defer compiler.Close()
	if err = compiler.Build(); err != nil {
		t.Error(err)
		return
	}

	if err = compiler.Publish(tnsClient); err != nil {
		t.Error(err)
		return
	}

	context := storage.Context{
		ProjectId: projectString,
		Matcher:   storageMatch1,
	}
	context.Context = service.Context()

	context2 := storage.Context{
		ProjectId: projectString,
		Matcher:   storageMatch2,
	}
	context2.Context = service.Context()

	context3 := storage.Context{
		ProjectId: projectString,
		Matcher:   storageMatch3,
	}
	context3.Context = service.Context()

	storage, err := service.Storage(context)
	if err != nil {
		t.Errorf("Creating new storage failed with: %s", err.Error())
		return
	}

	storage2, err := service.Storage(context2)
	if err != nil {
		t.Errorf("Creating new storage failed with: %s", err.Error())
		return
	}

	storage3, err := service.Storage(context3)
	if err != nil {
		t.Errorf("Creating new storage failed with: %s", err.Error())
		return
	}

	storage3Copy, err := service.Storage(context3)
	if err != nil {
		t.Errorf("Creating new storage failed with: %s", err.Error())
		return
	}

	if storage3.Kvdb() != storage3Copy.Kvdb() {
		t.Error("these storages should be pointing to the same database")
		return
	}

	if storage2.Kvdb() == storage3.Kvdb() {
		t.Error("these should not be the same kvdb")
		return
	}

	copyStorage, err := service.Storage(context)
	if err != nil {
		t.Errorf("Getting existing storage failed with: %s", err.Error())
		return
	}

	if copyStorage != storage {
		t.Errorf("These 2 storages should be exactly the same. \n %#v != %#v", copyStorage, storage)
		return
	}

	video1 := bytes.NewReader(sampleVideo)
	video2 := bytes.NewReader(sampleVideo2)

	// Add video1 as 'video'
	version, err := storage.AddFile(u.Context(), video1, "video", false)
	if err != nil {
		t.Error(err)
		return
	}

	// Video1 should be version 1 of "video"
	if version != 1 {
		t.Errorf("Expected version to be 1 got:%d", version)
		return
	}

	// Get "video" version 1
	outVideo, err := storage.Meta(u.Context(), "video", version)
	if err != nil {
		t.Error(err)
		return
	}

	if outVideo.Cid().String() != video1Cid {
		t.Errorf("Version not equal %s != %s", outVideo.Cid(), video1Cid)
		return
	}

	// Read "video" version 1
	outVideoFile, err := outVideo.Get()
	if err != nil {
		t.Error(err)
		return
	}

	outVideoBytes, err := io.ReadAll(outVideoFile)
	if err != nil {
		t.Error(err)
		return
	}

	// Compare "video" version 1 to video1
	if !bytes.Equal(sampleVideo, outVideoBytes) {
		t.Errorf("VIDEO ONE IS WRONG")
		return
	}

	// Add video2 as "video" version 2
	version, err = storage.AddFile(u.Context(), video2, "video", false)
	if err != nil {
		t.Error(err)
		return
	}

	// Expect video2 to be "video" version 2
	if version != 2 {
		t.Errorf("Expected version to be 2 got:%d", version)
		return
	}

	// Get "video" version 2
	outVideo, err = storage.Meta(u.Context(), "video", version)
	if err != nil {
		t.Error(err)
		return
	}

	if outVideo.Version() != 2 {
		t.Errorf("Expecting version to be 2 got %d", outVideo.Version())
		return
	}

	// Read "video" version 2
	outVideoFile, err = outVideo.Get()
	if err != nil {
		t.Error(err)
		return
	}

	outVideoBytes, err = io.ReadAll(outVideoFile)
	if err != nil {
		t.Error(err)
		return
	}

	// Compare "video" version 2 to video2
	if !bytes.Equal(sampleVideo2, outVideoBytes) {
		t.Errorf("VIDEO V2 IS WRONG should be video2")
		return
	}

	// Add video1 as "video" version 2
	version, err = storage.AddFile(u.Context(), video1, "video", true)
	if err != nil {
		t.Error(err)
		return
	}

	// Expect video1 to be "video" version 2
	if version != 2 {
		t.Errorf("Expected version to be 2 got:%d", version)
		return
	}

	// Get "video" version 2
	outVideo, err = storage.Meta(u.Context(), "video", version)
	if err != nil {
		t.Error(err)
		return
	}

	// Read "video" version 2
	outVideoFile, err = outVideo.Get()
	if err != nil {
		t.Error(err)
		return
	}

	outVideoBytes, err = io.ReadAll(outVideoFile)
	if err != nil {
		t.Error(err)
		return
	}

	entries, err := storage.List(u.Context(), "")
	assert.NilError(t, err)
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries got %d", len(entries))
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry, "/s/") {
			t.Errorf("size entry `%s` should not be in list", entry)
			return
		}
	}

	// Compare "video" version 2 to video1
	if !bytes.Equal(sampleVideo, outVideoBytes) {
		t.Errorf("VIDEO v2 IS WRONG should be video1")
		return
	}

	// Should delete latest version; 2
	err = storage.DeleteFile(u.Context(), "video", 0)
	assert.NilError(t, err)

	// gets Latest version of "video"
	version, err = storage.GetLatestVersion(u.Context(), "video")
	if err != nil {
		t.Error(err)
		return
	}

	// expect latest version to be 1 as previously we deleted the latest version and that should be 2
	if version != 1 {
		t.Errorf("Expected latest 'video' to be 1")
		return
	}

	// add video1 as "video" v2
	version, err = storage.AddFile(u.Context(), video1, "video", false)
	if err != nil {
		t.Error(err)
		return
	}

	// Expect video1 to be "video" version 2
	if version != 2 {
		t.Errorf("Expected version to be 2 got:%d", version)
		return
	}

	// Get "video" version 2
	outVideo, err = storage.Meta(u.Context(), "video", version)
	if err != nil {
		t.Error(err)
		return
	}

	outVideoFile, err = outVideo.Get()
	if err != nil {
		t.Error(err)
		return
	}

	// Read "video" version 2
	outVideoBytes, err = io.ReadAll(outVideoFile)
	if err != nil {
		t.Error(err)
		return
	}

	if !bytes.Equal(sampleVideo, outVideoBytes) {
		t.Errorf("VIDEO v2 IS WRONG should be video1")
		return
	}

	// add video1 as "video" v2
	version, err = storage.AddFile(u.Context(), video2, "video", false)
	if err != nil {
		t.Error(err)
		return
	}

	// Expect video2 to be "video" version 3
	if version != 3 {
		t.Errorf("Expected version to be 3 got:%d", version)
		return
	}

	// Get "video" version 3
	outVideo, err = storage.Meta(u.Context(), "video", version)
	if err != nil {
		t.Error(err)
		return
	}

	if outVideo.Cid().String() != video3Cid {
		t.Errorf("Version not equal %s != %s", outVideo.Cid(), video3Cid)
		return
	}

	outVideoFile, err = outVideo.Get()
	if err != nil {
		t.Error(err)
		return
	}

	// Read "video" version 3
	outVideoBytes, err = io.ReadAll(outVideoFile)
	if err != nil {
		t.Error(err)
		return
	}

	if !bytes.Equal(sampleVideo2, outVideoBytes) {
		t.Errorf("VIDEO v3 IS WRONG should be video1")
		return
	}

	// Should delete version 2
	err = storage.DeleteFile(u.Context(), "video", 2)
	assert.NilError(t, err)

	//Getting latest versions of video
	version, err = storage.GetLatestVersion(u.Context(), "video")
	if err != nil {
		t.Error(err)
		return
	}

	// Latest version should be 3
	if version != 3 {
		t.Errorf("EXPECTED v3 got v%d", version)
		return
	}

	// Attempt to delete all
	err = storage.DeleteFile(u.Context(), "video", -1)
	if err != nil {
		t.Error(err)
		return
	}

	// Get latest version should fail as all versions of video have been deleted
	_, err = storage.GetLatestVersion(u.Context(), "video")
	if err == nil {
		t.Error("EXPECTED LATEST TO FAIL")
		return
	}

	// Test Updating Size
	if storage.Capacity() != 50000000000 {
		t.Errorf("Starting capacity should be 50GB, got %d ", storage.Capacity())
		return
	}

	project, err := decompile.MockBuild(projectString, "",
		&structureSpec.Storage{
			Id:          "QmUhyzQ4sYGbTmFNY7w46syoiY6kYC6gCs3fDNzwMV1arH",
			Name:        "testStorage",
			Type:        "object",
			Description: "",
			Tags:        []string{"test"},
			Match:       "testStorage1",
			Regex:       true,
			Size:        2000000000,
		},
	)
	assert.NilError(t, err)

	meta.HeadCommit.ID = expectedCommitId

	rc, err = compile.CompilerConfig(project, meta, generatedDomainRegExp)
	assert.NilError(t, err)

	compiler, err = compile.New(rc, compile.Dev())
	assert.NilError(t, err)
	defer compiler.Close()

	err = compiler.Build()
	assert.NilError(t, err)

	err = compiler.Publish(tnsClient)
	assert.NilError(t, err)

	commitId, _, err := tnsClient.Simple().Commit(projectString, "master")
	assert.NilError(t, err)

	if commitId != expectedCommitId {
		t.Errorf("new commit id was not changed %s != %s", commitId, expectedCommitId)
		return
	}

	storage, err = service.Storage(context)
	if err != nil {
		t.Errorf("Creating new storage failed with: %s", err.Error())
		return
	}

	if storage.Capacity() != 2000000000 {
		t.Errorf("Size did not change %d != %d", storage.Capacity(), 2000000000)
		return
	}
}
