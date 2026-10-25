// Runs a Keeper on SQLite, node's own: node --test sdk/ts/test.

import assert from "node:assert/strict";
import { DatabaseSync } from "node:sqlite";
import { test } from "node:test";

import { answer, Keeper, Later, type Answer, type Task } from "../src/index.ts";

function task(key: string, until = Date.now() + 60_000): Task {
  return { key, attempt: 1, token: "t", until, keep: 0, step: "ship", shape: "ship-1", args: {} };
}

function keeper(): [Keeper, DatabaseSync] {
  const db = new DatabaseSync(":memory:");
  db.exec("CREATE TABLE shipped (id TEXT PRIMARY KEY)");
  const k = new Keeper(db);
  k.setup();
  return [k, db];
}

// A step that answers later keeps its claim: sent again, or probed,
// before its answer, it is under way, and does not act a second time; the
// answer kept with it, it gives that after.
test("a step that answers later acts once", async () => {
  const [k] = keeper();
  let acts = 0;
  const slow = async (): Promise<Answer> => {
    acts++;
    return answer.of(new Later());
  };
  assert.equal((await k.started(task("slow"), slow)).working, true);
  assert.deepEqual(await k.probe({ ...task("slow"), probe: true }), { working: true });
  assert.deepEqual(await k.started(task("slow"), slow), { working: true });
  assert.equal(acts, 1);
  await k.answered(task("slow"), answer.value({ Ok: "slow-1" }));
  assert.deepEqual(await k.started(task("slow"), slow), { value: { Ok: "slow-1" } });
});
