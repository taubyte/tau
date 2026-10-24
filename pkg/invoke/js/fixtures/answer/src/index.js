// A function on the js tier: it answers the method and the body it was sent, so
// a caller can tell the request reached it whole.
import { answer } from "./answer.js";

export default {
  async fetch(request) {
    return new Response(answer(request.method, await request.text()));
  },
};
