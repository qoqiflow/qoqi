<?php

declare(strict_types=1);

namespace App\Shop;

use App\Flow;
use App\Flow\Decline;
use Qoqi\Flow\Err;
use Qoqi\Flow\Ok;
use Qoqi\Flow\Result;
use Qoqi\Flow\Task;
use Qoqi\Flow\Undone;

/**
 * Charges cards, at a payment provider: an effect outside the shop's
 * database, kept under started, a claim written before it acts.
 */
final class Payments implements Flow\Payments
{
    public function __construct(private readonly \PDO $db)
    {
    }

    public function charge(string $customer, int $cents, Task $task): Undone|Err
    {
        if ($customer === 'mallory') {
            return new Err(new Decline\Fraud('the card was reported stolen'));
        }
        if ($cents > 100_000) {
            return new Err(new Decline\Insufficient());
        }
        // The provider, as it would be called: the step's key as its
        // idempotency key
        $count = Database::one($this->db, 'SELECT COUNT(*) FROM charges', []);
        $charge = sprintf('ch-%d', (is_int($count) ? $count : 0) + 1);
        $this->db->prepare('INSERT INTO charges (charge, customer) VALUES (?, ?)')->execute([$charge, $customer]);
        Database::say(sprintf('[payments] charges %s %d cents as %s', $customer, $cents, $charge));

        return new Undone($charge, $charge);
    }

    public function refund(string $charge, Task $task): Result
    {
        Database::say('[payments] refunds ' . $charge);

        return new Ok(null);
    }
}
