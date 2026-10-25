// A queue's secret, and what it signs.

import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import { test } from "node:test";

import { Queue, sign, verify } from "../src/index.ts";

// A secret of nothing is one anyone has: a queue is not made with one,
// and a request signed with one is refused.
test("a secret of nothing signs nothing", async () => {
  assert.throws(() => new Queue("billing", ""), /no secret/);
  assert.throws(() => new Queue("billing", process.env.QOQI_NO_SUCH_SECRET), /no secret/);
  const now = Date.now();
  // As anyone signs with a key of nothing
  const forged = `t=${now}, v1=${createHmac("sha256", "").update(`${now}\nPOST\n/q\nbilling\n{}`).digest("hex")}`;
  await assert.rejects(verify(forged, now, "POST", "/q", "billing", "{}", ["", "the real one"]));
  await verify(await sign("the real one", now, "POST", "/q", "billing", "{}"), now, "POST", "/q", "billing", "{}", ["", "the real one"]);
});
