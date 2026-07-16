//go:build database_empty

package main

//lint:file-ignore U1000 compiled file

import (
	"errors"
	"fmt"

	"github.com/taubyte/go-sdk/database"
	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
)

//export databaseemptytest
func databaseemptytest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	err = runDatabaseEmptyTest(h)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("Example test failed with: %s", err)))
		return 1
	}

	return 0
}

func runDatabaseEmptyTest(h http.Event) error {
	keystore, err := database.New("/test")
	if err != nil {
		return errors.New("Creating new database failed with:" + err.Error())
	}

	err = keystore.Put("test", nil)
	if err != nil {
		return errors.New("Keystore put failed with:" + err.Error())
	}

	data, err := keystore.Get("test")
	if err != nil {
		return errors.New("Keystore get failed with:" + err.Error())
	}

	if len(data) != 0 {
		return fmt.Errorf("expected no data got: %s", string(data))
	}

	_, err = h.Write([]byte("got the data for test"))
	return err
}
