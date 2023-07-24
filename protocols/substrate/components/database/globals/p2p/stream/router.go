package api

func (s *StreamHandler) setupRoutes() {
	router := s.stream.Router()

	router.AddStatic("list", s.listHandler)
	router.AddStatic("get", s.getHandler)
}
