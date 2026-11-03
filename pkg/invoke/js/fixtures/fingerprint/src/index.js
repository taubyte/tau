// Two functions on the js tier, each saying which value of signing_key it was given
// without answering with it: the default export with the fingerprint its build baked
// in, read through the helper, and reader with one of the value it reads at the call,
// through the vault module.
import { get } from "taubyte:secret/vault";
import { fingerprint } from "./fingerprint.js";
import { sha256 } from "./sha256.js";

export default {
  async fetch() {
    return new Response(fingerprint);
  },
};

export const reader = {
  async fetch() {
    return new Response(sha256(get("signing_key")).slice(0, 16));
  },
};
