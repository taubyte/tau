package accounts

import (
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// Verb names mirror services/accounts; duplicated to avoid importing the server.
const (
	verbAccount = "account"
	verbMember  = "member"
	verbUser    = "user"
	verbPlan    = "plan"
	verbLogin   = "login"
)

func readField(resp map[string]any, key string, out any) error {
	v, ok := resp[key]
	if !ok {
		return fmt.Errorf("response missing %q", key)
	}
	raw, err := cbor.Marshal(v)
	if err != nil {
		return fmt.Errorf("re-encode %s: %w", key, err)
	}
	if err := cbor.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s: %w", key, err)
	}
	return nil
}

func expectOK(resp map[string]any, op string) error {
	if ok, _ := resp["ok"].(bool); !ok {
		return errors.New(op + ": server did not confirm")
	}
	return nil
}
