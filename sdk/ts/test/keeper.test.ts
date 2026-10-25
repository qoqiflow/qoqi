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

// A step under once holds the connection's transaction while it acts: a
// claim written meanwhile for a step under started is not in it, and
// stays when the once step fails and rolls back.
test("a claim survives a step under once that fails", async () => {
  const [k, db] = keeper();
  let release!: () => void;
  const held = new Promise<void>((r) => (release = r));
  const failing = k.once(task("o"), async () => {
    db.prepare("INSERT INTO shipped (id) VALUES ('o')").run();
    await held;
    throw new Error("the carrier refused");
  });
  let acted = 0;
  const claimed = k.started(task("s"), async () => {
    acted++;
    return answer.value({ Ok: "s-1" });
  });
  release();
  await assert.rejects(failing, /the carrier refused/);
  assert.deepEqual(await claimed, { value: { Ok: "s-1" } });
  assert.equal(acted, 1);
  assert.deepEqual(await k.probe({ ...task("s"), probe: true }), { value: { Ok: "s-1" } });
  const shipped = db.prepare("SELECT COUNT(*) AS n FROM shipped WHERE id = 'o'").get() as { n: number };
  assert.equal(shipped.n, 0);
});
