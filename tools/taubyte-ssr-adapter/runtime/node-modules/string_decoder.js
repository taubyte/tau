// node:string_decoder shim — incremental bytes->string decoder over the
// platform TextDecoder (handles multi-byte chars split across chunks via the
// streaming option). Only utf8/utf-8 honor streaming; others decode per write.
//
// Authored as a classic function constructor (not an ES6 class): consumers such
// as iconv-lite inherit from it with `StringDecoder.call(this, enc)`, which
// throws on a class ("cannot be invoked without 'new'").

export function StringDecoder(encoding) {
  encoding = String(encoding == null ? "utf8" : encoding).toLowerCase().replace(/[-_]/g, "");
  this.encoding = encoding;
  this._dec = encoding === "utf8" ? new TextDecoder("utf-8") : null;
}

StringDecoder.prototype.write = function (buf) {
  if (buf == null) return "";
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
  if (this._dec) return this._dec.decode(bytes, { stream: true });
  const label = this.encoding === "latin1" || this.encoding === "binary" ? "latin1"
    : this.encoding === "ascii" ? "ascii"
    : this.encoding === "utf16le" || this.encoding === "ucs2" ? "utf-16le"
    : "utf-8";
  return new TextDecoder(label).decode(bytes);
};

StringDecoder.prototype.end = function (buf) {
  let out = "";
  if (buf != null) out += this.write(buf);
  if (this._dec) out += this._dec.decode(); // flush any pending bytes
  return out;
};

export default { StringDecoder };
