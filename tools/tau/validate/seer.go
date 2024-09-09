package validate

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func SeerFQDN(c context.Context, fqdn string) error {
	ctx, ctxC := context.WithTimeout(c, 10*time.Second)
	defer ctxC()

	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://seer.tau.%s/network/config", fqdn), nil)
	if err != nil {
		return fmt.Errorf("creating new http request failed with: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request to seer fqdn `%s` failed with: %w", fqdn, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("did not get 200 status code from seer got code: %d", resp.StatusCode)
	}

	return nil
}
