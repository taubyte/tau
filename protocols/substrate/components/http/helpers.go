package http

import "net/http"

func (s *Service) writeError(w http.ResponseWriter, err error) {
	w.Write([]byte(err.Error()))
	w.WriteHeader(500)
}
