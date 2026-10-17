<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow;

use Qoqi\Flow\Err;
use Qoqi\Flow\Result;
use Qoqi\Flow\Task;
use Qoqi\Flow\Undone;

/**
 * Carries out the steps of the queue warehouse. A step answers what it
 * returns; an exception is a failure of its body, but for Retry, Later
 * and Unknown of qoqi/flow.
 */
interface Warehouse
{
    /**
     * Gives back what a hold kept.
     *
     * step fn release(hold: Hold) -> Result<Void, Problem> by warehouse
     *
     * @return Result<null, Problem>
     */
    public function release(Hold $hold, Task $task): Result;

    /**
     * Holds the stock of the items for the order.
     *
     * step fn reserve(order: String, items: [Item]) -> Result<Hold, Problem> by warehouse undo release
     *
     * It returns, when it did its work, its value and what release is given;
     * an Err otherwise.
     *
     * @param list<Item> $items
     * @return Undone<Hold, Hold>|Err<Problem>
     */
    public function reserve(string $order, array $items, Task $task): Undone|Err;

    /**
     * Hands the parcel to the carrier: its tracking number.
     *
     * step fn ship(hold: Hold, to: String, speed: Speed) -> Result<String, Problem> by warehouse
     *
     * @return Result<string, Problem>
     */
    public function ship(Hold $hold, string $to, Speed $speed, Task $task): Result;
}
