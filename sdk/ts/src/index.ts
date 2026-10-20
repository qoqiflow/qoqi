// The library of a TypeScript application that carries out steps of
// Qoqi's flows: the daemon sends each step to the application's queue,
// signed, and the application answers what the step returns.
//
// `qoqi gen ts` writes the types of a program's steps, an interface
// for each queue, and the function that makes its handler; the
// application implements the interface, and serves the queue as any page:
//
//     const queue = billingQueue(new Billing(stripe), process.env.BILLING_SECRET!);
//     export default { fetch: (request: Request) => queue.fetch(request) };
//
// The handler takes a Request and gives a Response, as Fetch has them:
// Node (with ./node), Bun, Deno and workers run it.

/** A step the daemon sent. */
export interface Task {
  /** The same for every attempt, and no other step's: an idempotency key. */
  readonly key: string;
  /** From 1. */
  readonly attempt: number;
  /** Answers for the attempt, in what comes back later. */
  readonly token: string;
  /** When the daemon gives the attempt up, in milliseconds since 1970. */
  readonly until: number;
  /** How long the daemon keeps an ended instance, in milliseconds. */
  readonly keep: number;
  readonly step: string;
  readonly shape: string;
  /** The step's arguments, by name, in the form of the boundary. */
  readonly args: Record<string, unknown>;
  /** Asks what became of an attempt, rather than sending it. */
  readonly probe?: boolean;
  /** The attempt whose dead claim a person said did nothing. */
  readonly reclaim?: number;
}

/** Qoqi's Result<T, E>, as the boundary writes it. */
export type Result<T, E> = { Ok: T } | { Err: E };

export function ok<T>(value: T): { Ok: T } {
  return { Ok: value };
}

export function err<E>(error: E): { Err: E } {
  return { Err: error };
}

/** What a step answers, or a promise of it. */
export type Awaitable<T> = T | Promise<T>;

/**
 * What a step with an undo returns when it did its work: its value, Ok,
 * and the argument of the step that undoes it.
 */
export class Undone<T, U> {
  readonly value: T;
  readonly undo: U;

  constructor(value: T, undo: U) {
    this.value = value;
    this.undo = undo;
  }
}

/** Thrown by a step that did nothing, to be sent again after a while. */
export class Retry extends Error {
  readonly after: number;

  /** @param after milliseconds */
  constructor(reason: string, after = 1000) {
    super(reason);
    this.after = after;
  }
}

/** Thrown by a step whose answer comes later (Daemon.complete). */
export class Later extends Error {}

/** Thrown by a step that acted, and cannot say how. */
export class Unknown extends Error {}

/** What the application answers a step, as the daemon reads it. */
export interface Answer {
  value?: unknown;
  undo?: unknown;
  retry?: { reason: string; after: number };
  failed?: string;
  unknown?: boolean;
  working?: boolean;
  unknown_shape?: string;
  unseen?: boolean;
}

/** Marks the answer that comes later, on a request of its own: 202. */
const later = Symbol("later");

export const answer = {
  value: (value: unknown): Answer => ({ value }),
  withUndo: (value: unknown, undo: unknown): Answer => ({ value, undo }),
  failed: (why: string): Answer => ({ failed: why }),
  retry: (reason: string, after: number): Answer => ({ retry: { reason, after } }),
  unknown: (): Answer => ({ unknown: true }),
  later: (): Answer => ({ working: true, [later]: true }) as Answer,
  /** An attempt before acts under the key still: asked again later. */
  underWay: (): Answer => ({ working: true }),
  /** A probe of a key under which nothing was done. */
  unseen: (): Answer => ({ unseen: true }),

  /** The answer a step's exception gives. */
  of(e: unknown): Answer {
    if (e instanceof Retry) return answer.retry(e.message, e.after);
    if (e instanceof Unknown) return answer.unknown();
    if (e instanceof Later) return answer.later();
    if (e instanceof Error) return answer.failed(`${e.name}: ${e.message}`);
    return answer.failed(String(e));
  },
};

export type StepFn = (task: Task) => Awaitable<Answer>;

/** The header the daemon signs every request to a queue with. */
export const signatureHeader = "X-Flow-Signature";

/** How far from this clock a signature's moment may be, in milliseconds. */
export const skew = 5 * 60 * 1000;

const encoder = new TextEncoder();

async function mac(secret: string, at: number, method: string, path: string, queue: string, body: string): Promise<string> {
  const key = await crypto.subtle.importKey("raw", encoder.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const signed = await crypto.subtle.sign("HMAC", key, encoder.encode(`${at}\n${method}\n${path}\n${queue}\n${body}`));
  return Array.from(new Uint8Array(signed), (b) => b.toString(16).padStart(2, "0")).join("");
}

/**
 * The signature of a request to a queue: the moment, the method, the
 * path, the queue, then the body, each on a line of its own, with the
 * queue's secret, as the header writes it: "t=<ms>, v1=<hex>".
 */
export async function sign(secret: string, at: number, method: string, path: string, queue: string, body: string): Promise<string> {
  return `t=${at}, v1=${await mac(secret, at, method, path, queue, body)}`;
}

/** A request whose signature is wrong, missing, or too old. */
export class SignatureError extends Error {}

/** Checks a signature with any of the queue's secrets. */
export async function verify(header: string, now: number, method: string, path: string, queue: string, body: string, secrets: readonly string[]): Promise<void> {
  let at: number | undefined;
  const given: string[] = [];
  for (const part of header.split(",")) {
    const [key, value = ""] = part.trim().split("=", 2);
    if (key === "t" && /^\d+$/.test(value)) at = Number(value);
    if (key === "v1") given.push(value);
  }
  if (at === undefined || given.length === 0) throw new SignatureError(`${signatureHeader} is missing, or not t=..., v1=...`);
  if (Math.abs(now - at) > skew) throw new SignatureError("the request was signed too far from this clock");
  for (const secret of secrets) {
    const want = await mac(secret, at, method, path, queue, body);
    if (given.some((g) => same(g, want))) return;
  }
  throw new SignatureError("the request is not signed by the daemon");
}

/** Compares two texts in a time that does not depend on where they differ. */
function same(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

/**
 * Serves the steps of one queue: it checks each request's signature,
 * reads the step, and answers it with the handler of its shape; a GET is
 * answered the manifest, which says which shapes it knows.
 */
export class Queue {
  readonly name: string;
  readonly secrets: readonly string[];
  readonly #steps = new Map<string, StepFn>();
  readonly #once = new Map<string, Keeper>();
  readonly #started = new Map<string, Keeper>();
  // What keeps the steps not named, every step when only one is said
  #onceAll: Keeper | undefined;
  #startedAll: Keeper | undefined;

  constructor(name: string, ...secrets: string[]) {
    this.name = name;
    this.secrets = secrets;
  }

  /** Says what carries out the steps of a shape. */
  handle(shape: string, step: StepFn): void {
    this.#steps.set(shape, step);
  }

  /**
   * Keeps the answers of the queue's steps under once: their effect is in
   * the application's database, written with the Keeper's database, in
   * the transaction it holds with their answer. Without shapes, every
   * step of the queue; with them, those.
   */
  keepOnce(keeper: Keeper, ...shapes: string[]): this {
    if (shapes.length === 0) this.#onceAll = keeper;
    for (const shape of shapes) {
      this.#once.set(shape, keeper);
      this.#started.delete(shape);
    }
    return this;
  }

  /**
   * Keeps the answers of the queue's steps under started: a claim before
   * they act. Without shapes, every step of the queue; with them, those.
   */
  keepStarted(keeper: Keeper, ...shapes: string[]): this {
    if (shapes.length === 0) this.#startedAll = keeper;
    for (const shape of shapes) {
      this.#started.set(shape, keeper);
      this.#once.delete(shape);
    }
    return this;
  }

  /** How a shape's answers are kept: under once, under started, or neither. */
  #keeping(shape: string): [Keeper | undefined, Keeper | undefined] {
    const once = this.#once.get(shape);
    if (once !== undefined) return [once, undefined];
    const started = this.#started.get(shape);
    if (started !== undefined) return [undefined, started];
    if (!this.#steps.has(shape)) return [undefined, undefined];
    if (this.#onceAll !== undefined) return [this.#onceAll, undefined];
    return [undefined, this.#startedAll];
  }

  shapes(): string[] {
    return [...this.#steps.keys()].sort();
  }

  /** Answers a request, as Fetch has them. */
  async fetch(request: Request): Promise<Response> {
    const body = await request.text();
    const path = new URL(request.url).pathname;
    try {
      await verify(request.headers.get(signatureHeader) ?? "", Date.now(), request.method, path, this.name, body, this.secrets);
    } catch (e) {
      return json(401, { error: "signature", message: e instanceof Error ? e.message : String(e) });
    }
    if (request.method === "GET") {
      const shapes = this.shapes();
      return json(200, {
        shapes,
        once: shapes.filter((s) => this.#keeping(s)[0] !== undefined),
        started: shapes.filter((s) => this.#keeping(s)[1] !== undefined),
      });
    }
    if (request.method !== "POST") return json(405, { error: "method" });

    let task: Task;
    try {
      task = JSON.parse(body) as Task;
    } catch (e) {
      return json(400, { error: "step", message: String(e) });
    }
    const [once, started] = this.#keeping(task.shape);
    const keeper = started ?? once;
    if (task.probe) return json(200, keeper === undefined ? answer.unseen() : keeper.probe(task));
    const step = this.#steps.get(task.shape);
    if (step === undefined) return json(200, { unknown_shape: task.shape });
    const run = async (t: Task): Promise<Answer> => {
      try {
        return await step(t);
      } catch (e) {
        return answer.of(e);
      }
    };
    const said = once !== undefined ? await once.once(task, run) : started !== undefined ? await started.started(task, run) : await run(task);
    if (later in said) return new Response(null, { status: 202 });
    return json(200, said);
  }
}

function json(status: number, value: unknown): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

/** An answer the daemon did not take: 410 for an attempt given up. */
export class AnswerRefused extends Error {
  readonly status: number;

  constructor(status: number, said: string) {
    super(`the daemon answered ${status}: ${said}`);
    this.status = status;
  }

  get late(): boolean {
    return this.status === 410;
  }
}

/** Where the daemon answers, for the answers that come later. */
export class Daemon {
  readonly url: string;

  constructor(url: string) {
    this.url = url.replace(/\/$/, "");
  }

  /** Gives the answer of a step that threw Later: whether one was there already. */
  async complete(task: Task, said: Answer): Promise<boolean> {
    const response = await this.#post(task, said);
    const text = await response.text();
    if (response.status !== 200) throw new AnswerRefused(response.status, text);
    return (JSON.parse(text) as { already?: boolean }).already === true;
  }

  /** Asks whether the attempt is still under way: AnswerRefused, 410, once given up. */
  async working(task: Task): Promise<void> {
    const response = await this.#post(task, { working: true });
    if (response.status !== 200) throw new AnswerRefused(response.status, await response.text());
  }

  #post(task: Task, said: Answer): Promise<Response> {
    return fetch(`${this.url}/api/attempts`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key: task.key, attempt: task.attempt, token: task.token, ...said }),
    });
  }
}

/**
 * A database a Keeper keeps answers in: what node:sqlite's DatabaseSync
 * and better-sqlite3 give. Its queries quote the key column with double
 * quotes, as SQLite does, and Postgres: key is a reserved word elsewhere.
 */
export interface Database {
  exec(sql: string): unknown;
  prepare(sql: string): {
    run(...params: (string | number)[]): unknown;
    get(...params: (string | number)[]): unknown;
  };
}

interface Row {
  state: string;
  answer: string;
  beat: number;
}

/**
 * Keeps the answers of steps in the application's own database, keyed by
 * the step's key, so that a step sent again does not act twice: under
 * once, the effect and the answer in one transaction; under started, a
 * claim alive until the attempt's deadline, then the answer. A claim
 * found past its deadline without its answer is a process that died as
 * it acted: unknown, until a person says what happened.
 */
export class Keeper {
  readonly db: Database;
  readonly table: string;
  // Steps under once go one at a time: they share the connection, and its
  // transaction
  #turn: Promise<unknown> = Promise.resolve();

  constructor(db: Database, table = "flow_steps") {
    this.db = db;
    this.table = table;
  }

  /** Makes the table of answers, if it is not there. */
  setup(): void {
    this.db.exec(`CREATE TABLE IF NOT EXISTS ${this.table} ("key" TEXT PRIMARY KEY, state TEXT NOT NULL, answer TEXT NOT NULL, beat INTEGER NOT NULL)`);
  }

  #row(key: string): Row | undefined {
    return this.db.prepare(`SELECT state, answer, beat FROM ${this.table} WHERE "key" = ?`).get(key) as Row | undefined;
  }

  #kept(key: string): Answer | undefined {
    const row = this.#row(key);
    return row?.state === "answered" ? (JSON.parse(row.answer) as Answer) : undefined;
  }

  once(task: Task, step: (task: Task) => Promise<Answer>): Promise<Answer> {
    const run = async (): Promise<Answer> => {
      const kept = this.#kept(task.key);
      if (kept !== undefined) return kept;
      this.db.exec("BEGIN");
      let said: Answer;
      try {
        said = await step(task);
      } catch (e) {
        this.db.exec("ROLLBACK");
        throw e;
      }
      if (!("value" in said)) {
        // Only values are kept: a retry or a failure acts again next time
        this.db.exec("ROLLBACK");
        return this.#kept(task.key) ?? said;
      }
      try {
        this.db.prepare(`INSERT INTO ${this.table} ("key", state, answer, beat) VALUES (?, 'answered', ?, ?)`).run(task.key, JSON.stringify(said), Date.now());
        this.db.exec("COMMIT");
      } catch (e) {
        this.db.exec("ROLLBACK");
        return this.#kept(task.key) ?? answer.of(e);
      }
      return said;
    };
    const turn = this.#turn.then(run, run);
    this.#turn = turn.catch(() => undefined);
    return turn;
  }

  async started(task: Task, step: (task: Task) => Promise<Answer>): Promise<Answer> {
    const row = this.#row(task.key);
    if (row?.state === "answered") return JSON.parse(row.answer) as Answer;
    if (row !== undefined && Date.now() < row.beat) return answer.underWay();
    if (row !== undefined && !task.reclaim) return answer.unknown();
    if (row !== undefined) {
      // A person said the dead claim did nothing: it is dropped
      this.db.prepare(`DELETE FROM ${this.table} WHERE "key" = ? AND state = 'started'`).run(task.key);
    }
    try {
      this.db.prepare(`INSERT INTO ${this.table} ("key", state, answer, beat) VALUES (?, 'started', '', ?)`).run(task.key, task.until);
    } catch {
      // The loser of a race for the claim does not act
      return answer.underWay();
    }
    const said = await step(task);
    if (!("value" in said)) {
      this.db.prepare(`DELETE FROM ${this.table} WHERE "key" = ? AND state = 'started'`).run(task.key);
      return said;
    }
    this.db.prepare(`UPDATE ${this.table} SET state = 'answered', answer = ? WHERE "key" = ?`).run(JSON.stringify(said), task.key);
    return said;
  }

  /** What became of an attempt under its key. */
  probe(task: Task): Answer {
    const row = this.#row(task.key);
    if (row === undefined) return answer.unseen();
    if (row.state === "answered") return JSON.parse(row.answer) as Answer;
    if (Date.now() < row.beat) return answer.underWay();
    return answer.unknown();
  }

  /** Drops what is under a key: a person said what happened. */
  forget(key: string): void {
    this.db.prepare(`DELETE FROM ${this.table} WHERE "key" = ?`).run(key);
  }
}
