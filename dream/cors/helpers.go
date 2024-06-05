package cors

import (
	"fmt"
	"net/http"
	"net/url"
)

// Extract URL from request
func getURL(r *http.Request) (URL string, err error) {
	u, ok := r.URL.Query()["u"]
	if !ok || len(u[0]) < 1 {
		err = fmt.Errorf("URL is missing")
		return
	}

	URL = "https:/" + u[0]
	_, err = url.ParseRequestURI(string(URL))
	if err != nil {
		err = fmt.Errorf("400 - bad URL")
		return
	}

	return
}

func OutError(w http.ResponseWriter, code int, msg string) {
	w.Write([]byte(msg))
	w.WriteHeader(code)
}
