// A function in C++: it answers the language, the method, the path and the
// body, so a caller can tell which component served it.
#include <cstdlib>
#include <cstring>
#include <string>

#include "gen/handler.h"

static std::string text(const handler_string_t &s) {
	return std::string(reinterpret_cast<const char *>(s.ptr), s.len);
}

extern "C" void exports_handler_handle(handler_request_t *req, handler_response_t *ret) {
	std::string out = "c++ " + text(req->method) + " " + text(req->path) + " ";
	out.append(reinterpret_cast<const char *>(req->body.ptr), req->body.len);
	handler_request_free(req);

	auto *body = static_cast<uint8_t *>(std::malloc(out.size()));
	std::memcpy(body, out.data(), out.size());
	ret->status = 200;
	ret->headers = {nullptr, 0};
	ret->body = {body, out.size()};
}
