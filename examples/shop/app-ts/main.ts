// The shop's application, in TypeScript: it carries out the steps of the
// orders' flow, orders.qi, those of the warehouse and those of payments,
// over a database of SQLite, Node's own. The warehouse's steps change that
// database, and are kept under once: each writes its effect with the
// Keeper's database, in the transaction it holds with their answer. A
// charge reaches a payment provider, and is kept under started. Either
// way a step sent twice acts once.
//
//     SHOP_SECRET=dev node examples/shop/app-ts/main.ts --listen 127.0.0.1:8090

import { DatabaseSync } from "node:sqlite";
import { Keeper, Undone, err, ok } from "../../../sdk/ts/src/index.ts";
import type { Result, Task } from "../../../sdk/ts/src/index.ts";
import { listen } from "../../../sdk/ts/src/node.ts";
import { paymentsQueue, warehouseQueue } from "./steps.ts";
import type { Decline, Hold, Item, Payments, Problem, Speed, Warehouse } from "./steps.ts";

/** Holds stock for orders, and ships them. */
class Stock implements Warehouse {
  readonly #db: DatabaseSync;

  constructor(db: DatabaseSync) {
    this.#db = db;
  }

  reserve(order: string, items: Item[], task: Task): Undone<Hold, Hold> | { Err: Problem } {
    for (const item of items) {
      const left = this.#db.prepare("SELECT quantity FROM stock WHERE sku = ?").get(item.sku) as { quantity: number } | undefined;
      if (left === undefined || left.quantity < item.quantity) return err({ OutOfStock: item.sku });
    }
    for (const item of items) {
      this.#db.prepare("UPDATE stock SET quantity = quantity - ? WHERE sku = ?").run(item.quantity, item.sku);
    }
    const hold = { id: `hold-${order}` };
    this.#db.prepare("INSERT INTO holds (hold, items) VALUES (?, ?)").run(hold.id, JSON.stringify(items));
    console.error(`[warehouse] holds ${items.length} lines for order ${order}`);
    return new Undone(hold, hold);
  }

  release(hold: Hold): Result<null, Problem> {
    const row = this.#db.prepare("SELECT items FROM holds WHERE hold = ?").get(hold.id) as { items: string } | undefined;
    for (const item of row === undefined ? [] : (JSON.parse(row.items) as Item[])) {
      this.#db.prepare("UPDATE stock SET quantity = quantity + ? WHERE sku = ?").run(item.quantity, item.sku);
    }
    this.#db.prepare("DELETE FROM holds WHERE hold = ?").run(hold.id);
    console.error(`[warehouse] releases ${hold.id}`);
    return ok(null);
  }

  ship(hold: Hold, to: string, speed: Speed): Result<string, Problem> {
    if (to === "nowhere") return err({ Undeliverable: `no carrier goes to ${to}` });
    const { n } = this.#db.prepare("SELECT COUNT(*) AS n FROM parcels").get() as { n: number };
    const parcel = typeof speed === "object" ? `express-${n + 1}-${speed.Express}d` : `parcel-${n + 1}`;
    this.#db.prepare("INSERT INTO parcels (parcel, hold) VALUES (?, ?)").run(parcel, hold.id);
    console.error(`[warehouse] ships ${hold.id} to ${to} as ${parcel}`);
    return ok(parcel);
  }
}

/** Charges cards at a payment provider, which a table of charges stands for here. */
class Cards implements Payments {
  readonly #db: DatabaseSync;

  constructor(db: DatabaseSync) {
    this.#db = db;
  }

  charge(customer: string, cents: number): Undone<string, string> | { Err: Decline } {
    if (customer === "mallory") return err({ Fraud: "the card was reported stolen" });
    if (cents > 100_000) return err("Insufficient");
    const { n } = this.#db.prepare("SELECT COUNT(*) AS n FROM charges").get() as { n: number };
    const charge = `ch-${n + 1}`;
    this.#db.prepare("INSERT INTO charges (charge, customer) VALUES (?, ?)").run(charge, customer);
    console.error(`[payments] charges ${customer} ${cents} cents as ${charge}`);
    return new Undone(charge, charge);
  }

  refund(charge: string): Result<null, Decline> {
    console.error(`[payments] refunds ${charge}`);
    return ok(null);
  }
}

const secret = process.env.SHOP_SECRET;
if (!secret) {
  console.error("SHOP_SECRET is the secret the daemon signs the steps with, as qoqi.json says");
  process.exit(1);
}
const at = process.argv.indexOf("--listen");
const address = at >= 0 ? process.argv[at + 1] : undefined;
const [host = "127.0.0.1", port = "8090"] = (address ?? "127.0.0.1:8090").split(":");

const db = new DatabaseSync(process.env.SHOP_DB ?? "shop.db");
db.exec(`
  CREATE TABLE IF NOT EXISTS stock (sku TEXT PRIMARY KEY, quantity INTEGER NOT NULL);
  CREATE TABLE IF NOT EXISTS holds (hold TEXT PRIMARY KEY, items TEXT NOT NULL);
  CREATE TABLE IF NOT EXISTS parcels (parcel TEXT PRIMARY KEY, hold TEXT NOT NULL);
  CREATE TABLE IF NOT EXISTS charges (charge TEXT PRIMARY KEY, customer TEXT NOT NULL);
  INSERT OR IGNORE INTO stock (sku, quantity) VALUES ('mug', 10), ('lamp', 1);
`);
const keeper = new Keeper(db);
keeper.setup();

// The warehouse's steps change the shop's database: under once. A charge
// reaches the provider: under started
const warehouse = warehouseQueue(new Stock(db), secret).keepOnce(keeper);
const payments = paymentsQueue(new Cards(db), secret).keepStarted(keeper);

listen(
  {
    "/warehouse": (request) => warehouse.fetch(request),
    "/payments": (request) => payments.fetch(request),
  },
  Number(port),
  host,
);
console.error(`the shop serves its queues at http://${host}:${port}/warehouse and /payments`);
