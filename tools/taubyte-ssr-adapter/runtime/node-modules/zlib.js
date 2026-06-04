// node:zlib stub. Compression isn't wired in the sandbox; HTTP frameworks treat
// it as optional (content negotiation), so the streaming constructors exist (as
// pass-through transforms) and the one-shot helpers report failure via callback
// rather than crashing the bundle at import time.

import { PassThrough } from "stream";

// Streaming codecs as pass-throughs (identity). Frameworks fall back to
// uncompressed when the client doesn't require encoding.
export const createGzip = () => new PassThrough();
export const createGunzip = () => new PassThrough();
export const createDeflate = () => new PassThrough();
export const createInflate = () => new PassThrough();
export const createBrotliCompress = () => new PassThrough();
export const createBrotliDecompress = () => new PassThrough();

function unsupportedAsync(name) {
  return function (buf, optsOrCb, maybeCb) {
    const cb = typeof optsOrCb === "function" ? optsOrCb : maybeCb;
    const err = new Error("node:zlib." + name + " is not available in the Taubyte sandbox");
    if (typeof cb === "function") cb(err);
    else throw err;
  };
}

export const gzip = unsupportedAsync("gzip");
export const gunzip = unsupportedAsync("gunzip");
export const deflate = unsupportedAsync("deflate");
export const inflate = unsupportedAsync("inflate");
export const brotliCompress = unsupportedAsync("brotliCompress");
export const brotliDecompress = unsupportedAsync("brotliDecompress");

function unsupportedSync(name) {
  return function () { throw new Error("node:zlib." + name + " is not available in the Taubyte sandbox"); };
}
export const gzipSync = unsupportedSync("gzipSync");
export const gunzipSync = unsupportedSync("gunzipSync");
export const deflateSync = unsupportedSync("deflateSync");
export const inflateSync = unsupportedSync("inflateSync");

export const constants = { Z_NO_FLUSH: 0, Z_SYNC_FLUSH: 2, Z_FINISH: 4, Z_OK: 0 };

export default {
  createGzip, createGunzip, createDeflate, createInflate, createBrotliCompress, createBrotliDecompress,
  gzip, gunzip, deflate, inflate, brotliCompress, brotliDecompress,
  gzipSync, gunzipSync, deflateSync, inflateSync, constants,
};
