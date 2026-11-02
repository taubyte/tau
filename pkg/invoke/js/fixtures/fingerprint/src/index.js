// A function on the js tier answering with the fingerprint its build baked in. The
// build read a secret through the helper, so the answer says which value it was given.
import { fingerprint } from "./fingerprint.js";

export default {
  async fetch() {
    return new Response(fingerprint);
  },
};
