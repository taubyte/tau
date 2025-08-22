package seer

import (
	"testing"

	"github.com/spf13/afero"
)

func TestSync(t *testing.T) {

	//Create first seer
	var AppFs = afero.NewMemMapFs()
	seer, err := New(VirtualFS(AppFs, "/"))
	if err != nil {
		t.Error(err)
	}

	err = seer.Get("file").Get("file2").Document().Set("inDocument").Commit()
	seer.Get("file").Get("file3").Document().Commit()
	seer.Get("file").Get("file4").Document().Commit()
	if err != nil {
		t.Errorf("Creating document failed with error: %s", err.Error())
	}

	err = seer.Sync()
	if err != nil {
		t.Error("Failed syncing. ", err)
	}
	listItems, err := seer.Get("file").List()
	if err != nil {
		t.Errorf("List failed with error: %s", err.Error())
	}
	if listItems[0] != "file2" {
		t.Error("Did not find the created file")
	}
	if len(listItems) != 3 {
		t.Error("Did not find all the files", err)
	}

	//Create second seer
	seer2, err := New(VirtualFS(AppFs, "/"))
	if err != nil {
		t.Error(err)
	}

	seer2List, err := seer2.Get("file").List()
	if err != nil {
		t.Error(err)
	}

	if seer2List[0] != "file2" {
		t.Errorf("Not getting the file")
	}

	if len(seer2List) != 3 {
		t.Error("Did not find all the files", err)
	}

	var value string
	seer2.Get("file").Get("file2").Value(&value)
	if value != "inDocument" {
		t.Error("Did not find inDocument. ", err)
	}
}
