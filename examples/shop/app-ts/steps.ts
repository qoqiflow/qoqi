// Written by qoqi gen from orders.qi: change the flow, not this file.

import { Queue, Undone, answer } from "../../../sdk/ts/src/index.ts";
import type { Awaitable, Result, Task } from "../../../sdk/ts/src/index.ts";

/** Why a card is not charged. */
export type Decline =
  | "Insufficient"
  | { Fraud: string }
  | "Expired";

/** What the warehouse holds for an order until it ships. */
export interface Hold {
  id: string;
}

export type Problem =
  | { OutOfStock: string }
  | { Declined: Decline }
  | { Undeliverable: string };

export interface Item {
  sku: string;
  quantity: number;
  cents: number;
}

/** How fast a parcel goes: express in so many days. */
export type Speed =
  | "Standard"
  | { Express: number };

/**
 * Carries out the steps of the queue payments. A step answers what it
 * returns; an exception is a failure of its body, but for Retry, Later
 * and Unknown of @qoqi/flow.
 */
export interface Payments {
  /**
   * Charges the customer's card, the step's key as the idempotency key. The bank has half a minute to answer.
   *
   * step fn charge(customer: String, cents: Int) -> Result<String, Decline> by payments undo refund
   *
   * It returns, when it did its work, its value and what refund is given;
   * an Err otherwise.
   */
  charge(customer: string, cents: number, task: Task): Awaitable<Undone<string, string> | { Err: Decline }>;

  /**
   * Gives the money of a charge back.
   *
   * step fn refund(charge: String) -> Result<Void, Decline> by payments
   */
  refund(charge: string, task: Task): Awaitable<Result<null, Decline>>;
}

/** The shapes of the steps of payments: which handler answers which step. */
export const paymentsShapes = {
  charge: "999298825e404c21",
  refund: "6ac84f977b4139fe",
} as const;

/** Makes the handler of the queue payments, at the address qoqi.json gives it. */
export function paymentsQueue(steps: Payments, ...secrets: string[]): Queue {
  const queue = new Queue("payments", ...secrets);

  queue.handle(paymentsShapes.charge, async (task) => {
    const result = await steps.charge(task.args.customer as string, task.args.cents as number, task);
    if (result instanceof Undone) return answer.withUndo({ Ok: result.value }, result.undo);
    return answer.value(result);
  });

  queue.handle(paymentsShapes.refund, async (task) => {
    return answer.value(await steps.refund(task.args.charge as string, task));
  });

  return queue;
}

/**
 * Carries out the steps of the queue warehouse. A step answers what it
 * returns; an exception is a failure of its body, but for Retry, Later
 * and Unknown of @qoqi/flow.
 */
export interface Warehouse {
  /**
   * Gives back what a hold kept.
   *
   * step fn release(hold: Hold) -> Result<Void, Problem> by warehouse
   */
  release(hold: Hold, task: Task): Awaitable<Result<null, Problem>>;

  /**
   * Holds the stock of the items for the order.
   *
   * step fn reserve(order: String, items: [Item]) -> Result<Hold, Problem> by warehouse undo release
   *
   * It returns, when it did its work, its value and what release is given;
   * an Err otherwise.
   */
  reserve(order: string, items: Item[], task: Task): Awaitable<Undone<Hold, Hold> | { Err: Problem }>;

  /**
   * Hands the parcel to the carrier: its tracking number.
   *
   * step fn ship(hold: Hold, to: String, speed: Speed) -> Result<String, Problem> by warehouse
   */
  ship(hold: Hold, to: string, speed: Speed, task: Task): Awaitable<Result<string, Problem>>;
}

/** The shapes of the steps of warehouse: which handler answers which step. */
export const warehouseShapes = {
  release: "2b90f4d358ae69fa",
  reserve: "f2aac7a6deff1975",
  ship: "03f2e357d2b665db",
} as const;

/** Makes the handler of the queue warehouse, at the address qoqi.json gives it. */
export function warehouseQueue(steps: Warehouse, ...secrets: string[]): Queue {
  const queue = new Queue("warehouse", ...secrets);

  queue.handle(warehouseShapes.release, async (task) => {
    return answer.value(await steps.release(task.args.hold as Hold, task));
  });

  queue.handle(warehouseShapes.reserve, async (task) => {
    const result = await steps.reserve(task.args.order as string, task.args.items as Item[], task);
    if (result instanceof Undone) return answer.withUndo({ Ok: result.value }, result.undo);
    return answer.value(result);
  });

  queue.handle(warehouseShapes.ship, async (task) => {
    return answer.value(await steps.ship(task.args.hold as Hold, task.args.to as string, task.args.speed as Speed, task));
  });

  return queue;
}
