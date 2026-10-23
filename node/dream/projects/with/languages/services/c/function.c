// A function in C: it answers the language, the method, the path and the
// body, so a caller can tell which component served it.
#include <stdlib.h>
#include <string.h>

#include "gen/handler.h"

static size_t put(uint8_t *at, const uint8_t *from, size_t n) {
	memcpy(at, from, n);
	return n;
}

void exports_handler_handle(handler_request_t *req, handler_response_t *ret) {
	size_t n = 2 + req->method.len + 1 + req->path.len + 1 + req->body.len;
	uint8_t *body = malloc(n);
	size_t at = put(body, (const uint8_t *)"c ", 2);
	at += put(body + at, req->method.ptr, req->method.len);
	body[at++] = ' ';
	at += put(body + at, req->path.ptr, req->path.len);
	body[at++] = ' ';
	put(body + at, req->body.ptr, req->body.len);

	ret->status = 200;
	ret->headers.ptr = NULL;
	ret->headers.len = 0;
	ret->body.ptr = body;
	ret->body.len = n;
	handler_request_free(req);
}
