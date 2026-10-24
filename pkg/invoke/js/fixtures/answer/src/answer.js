// A module of its own, so the build has something to bundle.
export function answer(method, body) {
  return `${method} ${body}`;
}
