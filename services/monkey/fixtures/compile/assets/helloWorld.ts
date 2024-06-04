import { Event } from "sdk/event";

export function doStuff(e: Event): u32 {
  let httpEvent = e.http();
  console.log(e.toString());
  if (httpEvent == null) {
    return 1;
  }

  let resp = httpEvent.unwrap().write("Hello, world!");
  if (resp.err) {
    console.log(resp.err);
    return 1;
  }

  console.log(resp.unwrap().toString());

  return 0;
}
