package seer

import (
	"os"
	"testing"
)

func TestGet(t *testing.T) {
	defer os.RemoveAll("fakeroot")
	os.Mkdir("fakeroot", 0755)

	t.Run("Creating a SystemFS", func(t *testing.T) {
		seer, err := New(SystemFS("fakeroot"))
		if err != nil {
			t.Error(err)
			return
		}

		t.Run("create and get document", func(t *testing.T) {
			seer.Get("parents").Get("p").Get("test").Document().Commit()
			if err != nil {
				t.Error("Error Committing", err)
			}

			listFile, err := seer.Get("parents").Get("p").List()
			if err != nil {
				t.Error("Error listing files. ", err)
			}

			if listFile[0] != "test" {
				t.Error("Failed getting the test yaml file.")
			}

			t.Run("get value inside a document", func(t *testing.T) {
				var val string
				err = seer.Get("parents").Get("sibling").Get("test").Document().Set("inside").Commit()
				if err != nil {
					t.Error("Failed committing inside document. ", err)
				}

				err = seer.Get("parents").Get("sibling").Get("test").Value(&val)
				if err != nil {
					t.Error("Failed getting value inside document. ", err)
				}

				if val != "inside" {
					t.Error("Val does not = inside. ", err)
				}
			})

		})
	})
}
