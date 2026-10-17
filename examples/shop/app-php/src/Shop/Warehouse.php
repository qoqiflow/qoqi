<?php

declare(strict_types=1);

namespace App\Shop;

use App\Flow;
use App\Flow\Hold;
use App\Flow\Problem;
use App\Flow\Speed;
use Qoqi\Flow\Err;
use Qoqi\Flow\Ok;
use Qoqi\Flow\Result;
use Qoqi\Flow\Task;
use Qoqi\Flow\Undone;

/**
 * Holds stock for orders, and ships them. Its steps are kept under once:
 * what each does is written in the shop's database with its answer, in
 * the transaction the Keeper holds, so that one sent again acts once.
 */
final class Warehouse implements Flow\Warehouse
{
    public function __construct(private readonly \PDO $db)
    {
    }

    public function reserve(string $order, array $items, Task $task): Undone|Err
    {
        foreach ($items as $item) {
            $left = Database::one($this->db, 'SELECT quantity FROM stock WHERE sku = ?', [$item->sku]);
            if (!is_int($left) || $left < $item->quantity) {
                return new Err(new Problem\OutOfStock($item->sku));
            }
        }
        foreach ($items as $item) {
            $this->db->prepare('UPDATE stock SET quantity = quantity - ? WHERE sku = ?')
                ->execute([$item->quantity, $item->sku]);
        }
        $hold = new Hold('hold-' . $order);
        $lines = json_encode(array_map(static fn (Flow\Item $item): object => $item->toPlain(), $items));
        $this->db->prepare('INSERT INTO holds (hold, items) VALUES (?, ?)')->execute([$hold->id, (string) $lines]);
        Database::say(sprintf('[warehouse] holds %d lines for order %s', count($items), $order));

        return new Undone($hold, $hold);
    }

    public function release(Hold $hold, Task $task): Result
    {
        $lines = Database::one($this->db, 'SELECT items FROM holds WHERE hold = ?', [$hold->id]);
        $items = is_string($lines) ? json_decode($lines, true) : [];
        foreach (is_array($items) ? $items : [] as $item) {
            $item = Flow\Item::fromPlain($item);
            $this->db->prepare('UPDATE stock SET quantity = quantity + ? WHERE sku = ?')
                ->execute([$item->quantity, $item->sku]);
        }
        $this->db->prepare('DELETE FROM holds WHERE hold = ?')->execute([$hold->id]);
        Database::say('[warehouse] releases ' . $hold->id);

        return new Ok(null);
    }

    public function ship(Hold $hold, string $to, Speed $speed, Task $task): Result
    {
        if ($to === 'nowhere') {
            return new Err(new Problem\Undeliverable('no carrier goes to ' . $to));
        }
        $count = Database::one($this->db, 'SELECT COUNT(*) FROM parcels', []);
        $number = (is_int($count) ? $count : 0) + 1;
        $parcel = $speed instanceof Speed\Express
            ? sprintf('express-%d-%dd', $number, $speed->value)
            : sprintf('parcel-%d', $number);
        $this->db->prepare('INSERT INTO parcels (parcel, hold) VALUES (?, ?)')->execute([$parcel, $hold->id]);
        Database::say(sprintf('[warehouse] ships %s to %s as %s', $hold->id, $to, $parcel));

        return new Ok($parcel);
    }
}
