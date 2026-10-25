// Serves a queue with node:http, and sends it what a client should not.

import assert from "node:assert/strict";
import { request } from "node:http";
import type { AddressInfo } from "node:net";
import { test } from "node:test";

import { listen, maxBody } from "../src/node.ts";

function send(port: number, headers: Record<string, string>, body = ""): Promise<number> {
  return new Promise((resolve, reject) => {
    const req = request({ host: "127.0.0.1", port, path: "/q", method: "POST", headers, setHost: false }, (res) => {
      res.resume();
      resolve(res.statusCode ?? 0);
    });
    req.on("error", reject);
    req.end(body);
  });
}

// A Host that is no host, a handler that throws, a body too large: each
// is answered, and the server serves the next request.
test("the server outlives what it is sent", async () => {
  let thrown = false;
  const server = listen(
    {
      "/q": async (r) => {
        if (r.headers.get("x-throw") !== null && !thrown) {
          thrown = true;
          throw new Error("the handler failed");
        }
        return new Response("ok");
      },
    },
    0,
  );
  await new Promise<void>((r) => server.once("listening", () => r()));
  const port = (server.address() as AddressInfo).port;
  try {
    assert.equal(await send(port, { Host: "[bad" }), 200);
    assert.equal(await send(port, { Host: "localhost", "X-Throw": "1" }), 500);
    assert.equal(await send(port, { Host: "localhost" }, "x".repeat(maxBody + 1)), 413);
    assert.equal(await send(port, { Host: "localhost" }), 200);
  } finally {
    server.close();
  }
});
