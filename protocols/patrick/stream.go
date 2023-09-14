package service

func (h *PatrickService) setupStreamRoutes() {
	h.stream.Define("patrick", h.requestServiceHandler)
}
