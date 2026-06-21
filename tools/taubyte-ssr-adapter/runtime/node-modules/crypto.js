// node:crypto shim. Randomness comes from the platform WebCrypto
// (globalThis.crypto, StarlingMonkey-native). createHash provides synchronous
// SHA-1/SHA-256 in pure JS (Node's createHash is sync; WebCrypto's digest is
// async, so it can't back it) — enough for `etag` and similar content hashing.

const webcrypto = globalThis.crypto;

export function randomBytes(size, cb) {
  const buf = new Uint8Array(size);
  webcrypto.getRandomValues(buf);
  const out = typeof Buffer !== "undefined" ? Buffer.from(buf) : buf;
  if (cb) { cb(null, out); return; }
  return out;
}

export function randomFillSync(buf) {
  webcrypto.getRandomValues(buf);
  return buf;
}

export function randomUUID() {
  return webcrypto.randomUUID();
}

export function randomInt(min, max) {
  if (max === undefined) { max = min; min = 0; }
  const range = max - min;
  const buf = new Uint32Array(1);
  webcrypto.getRandomValues(buf);
  return min + (buf[0] % range);
}

// --- synchronous hashing ---------------------------------------------------

function toBytes(data, enc) {
  if (data instanceof Uint8Array) return data;
  if (data instanceof ArrayBuffer) return new Uint8Array(data);
  if (typeof data === "string") {
    if (enc === "hex") {
      const out = new Uint8Array(data.length / 2);
      for (let i = 0; i < out.length; i++) out[i] = parseInt(data.substr(i * 2, 2), 16);
      return out;
    }
    if (enc === "base64") {
      const bin = atob(data);
      const out = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
      return out;
    }
    return new TextEncoder().encode(data);
  }
  return new Uint8Array(0);
}

function encodeDigest(bytes, enc) {
  if (enc === "hex") {
    let s = "";
    for (const b of bytes) s += b.toString(16).padStart(2, "0");
    return s;
  }
  if (enc === "base64") {
    let bin = "";
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin);
  }
  return typeof Buffer !== "undefined" ? Buffer.from(bytes) : bytes;
}

function sha1(bytes) {
  const ml = bytes.length * 8;
  const withOne = bytes.length + 1;
  const total = withOne + ((56 - (withOne % 64) + 64) % 64) + 8;
  const msg = new Uint8Array(total);
  msg.set(bytes);
  msg[bytes.length] = 0x80;
  const dv = new DataView(msg.buffer);
  dv.setUint32(total - 4, ml >>> 0);
  dv.setUint32(total - 8, Math.floor(ml / 0x100000000));
  let h0 = 0x67452301, h1 = 0xEFCDAB89, h2 = 0x98BADCFE, h3 = 0x10325476, h4 = 0xC3D2E1F0;
  const w = new Uint32Array(80);
  for (let off = 0; off < total; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = dv.getUint32(off + i * 4);
    for (let i = 16; i < 80; i++) { const v = w[i-3]^w[i-8]^w[i-14]^w[i-16]; w[i] = (v<<1)|(v>>>31); }
    let a=h0,b=h1,c=h2,d=h3,e=h4;
    for (let i = 0; i < 80; i++) {
      let f, k;
      if (i<20){f=(b&c)|(~b&d);k=0x5A827999;}
      else if(i<40){f=b^c^d;k=0x6ED9EBA1;}
      else if(i<60){f=(b&c)|(b&d)|(c&d);k=0x8F1BBCDC;}
      else{f=b^c^d;k=0xCA62C1D6;}
      const t=(((a<<5)|(a>>>27))+f+e+k+w[i])>>>0;
      e=d;d=c;c=(b<<30)|(b>>>2);b=a;a=t;
    }
    h0=(h0+a)>>>0;h1=(h1+b)>>>0;h2=(h2+c)>>>0;h3=(h3+d)>>>0;h4=(h4+e)>>>0;
  }
  const out = new Uint8Array(20);
  new DataView(out.buffer).setUint32(0,h0); new DataView(out.buffer).setUint32(4,h1);
  new DataView(out.buffer).setUint32(8,h2); new DataView(out.buffer).setUint32(12,h3);
  new DataView(out.buffer).setUint32(16,h4);
  return out;
}

const K256 = new Uint32Array([
  0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
  0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
  0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
  0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
  0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
  0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
  0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
  0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2]);

function sha256(bytes) {
  const ml = bytes.length * 8;
  const withOne = bytes.length + 1;
  const total = withOne + ((56 - (withOne % 64) + 64) % 64) + 8;
  const msg = new Uint8Array(total);
  msg.set(bytes); msg[bytes.length] = 0x80;
  const dv = new DataView(msg.buffer);
  dv.setUint32(total - 4, ml >>> 0);
  dv.setUint32(total - 8, Math.floor(ml / 0x100000000));
  let h=[0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19];
  const w = new Uint32Array(64);
  const rotr=(x,n)=>(x>>>n)|(x<<(32-n));
  for (let off=0; off<total; off+=64) {
    for (let i=0;i<16;i++) w[i]=dv.getUint32(off+i*4);
    for (let i=16;i<64;i++){
      const s0=rotr(w[i-15],7)^rotr(w[i-15],18)^(w[i-15]>>>3);
      const s1=rotr(w[i-2],17)^rotr(w[i-2],19)^(w[i-2]>>>10);
      w[i]=(w[i-16]+s0+w[i-7]+s1)>>>0;
    }
    let [a,b,c,d,e,f,g,hh]=h;
    for (let i=0;i<64;i++){
      const S1=rotr(e,6)^rotr(e,11)^rotr(e,25);
      const ch=(e&f)^(~e&g);
      const t1=(hh+S1+ch+K256[i]+w[i])>>>0;
      const S0=rotr(a,2)^rotr(a,13)^rotr(a,22);
      const maj=(a&b)^(a&c)^(b&c);
      const t2=(S0+maj)>>>0;
      hh=g;g=f;f=e;e=(d+t1)>>>0;d=c;c=b;b=a;a=(t1+t2)>>>0;
    }
    h=[(h[0]+a)>>>0,(h[1]+b)>>>0,(h[2]+c)>>>0,(h[3]+d)>>>0,(h[4]+e)>>>0,(h[5]+f)>>>0,(h[6]+g)>>>0,(h[7]+hh)>>>0];
  }
  const out=new Uint8Array(32); const odv=new DataView(out.buffer);
  for(let i=0;i<8;i++) odv.setUint32(i*4,h[i]);
  return out;
}

class Hash {
  constructor(algorithm) {
    this.algorithm = String(algorithm).toLowerCase();
    this._chunks = [];
  }
  update(data, enc) { this._chunks.push(toBytes(data, enc)); return this; }
  digest(enc) {
    let len = 0; for (const c of this._chunks) len += c.length;
    const all = new Uint8Array(len); let o = 0;
    for (const c of this._chunks) { all.set(c, o); o += c.length; }
    let d;
    if (this.algorithm === "sha1") d = sha1(all);
    else if (this.algorithm === "sha256") d = sha256(all);
    else throw new Error("crypto: unsupported hash algorithm '" + this.algorithm + "' (shim provides sha1, sha256)");
    return encodeDigest(d, enc);
  }
}

export function createHash(algorithm) {
  return new Hash(algorithm);
}

export function createHmac() {
  throw new Error("crypto.createHmac is not supported in the Taubyte node shim");
}

export const webcrypto_ = webcrypto;
export { webcrypto_ as webcrypto };
export const subtle = webcrypto && webcrypto.subtle;
export function getRandomValues(arr) { return webcrypto.getRandomValues(arr); }

export default {
  randomBytes, randomFillSync, randomUUID, randomInt, createHash, createHmac,
  webcrypto, subtle, getRandomValues,
};
