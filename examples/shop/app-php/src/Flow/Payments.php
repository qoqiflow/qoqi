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
 * Carries out the steps of the queue payments. A step answers what it
 * returns; an exception is a failure of its body, but for Retry, Later
 * and Unknown of qoqi/flow.
 */
interface Payments
{
    /**
     * Charges the customer's card, the step's key as the idempotency key. The bank has half a minute to answer.
     *
     * step fn charge(customer: String, cents: Int) -> Result<String, Decline> by payments undo refund
     *
     * It returns, when it did its work, its value and what refund is given;
     * an Err otherwise.
     *
     * @return Undone<string, string>|Err<Decline>
     */
    public function charge(string $customer, int $cents, Task $task): Undone|Err;

    /**
     * Gives the money of a charge back.
     *
     * step fn refund(charge: String) -> Result<Void, Decline> by payments
     *
     * @return Result<null, Decline>
     */
    public function refund(string $charge, Task $task): Result;
}
