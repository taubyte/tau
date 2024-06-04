package api

func (s *StreamHandler) setupRoutes() {

	s.stream.Define("list", s.listHandler)
	s.stream.Define("get", s.getHandler)
}
