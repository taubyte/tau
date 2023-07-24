package service

func (h *PatrickService) setupStreamRoutes() {
	h.stream.Router().AddStatic("patrick", h.requestServiceHandler)
}
