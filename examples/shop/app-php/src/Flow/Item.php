<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow;

use Qoqi\Flow\Plain;

final class Item
{
    public function __construct(
        public readonly string $sku,
        public readonly int $quantity,
        public readonly int $cents,
    ) {
    }

    public static function fromPlain(mixed $plain): self
    {
        $fields = Plain::fields($plain);

        return new self(
            sku: Plain::string($fields['sku'] ?? null),
            quantity: Plain::int($fields['quantity'] ?? null),
            cents: Plain::int($fields['cents'] ?? null),
        );
    }

    public function toPlain(): object
    {
        return (object) [
            'sku' => $this->sku,
            'quantity' => $this->quantity,
            'cents' => $this->cents,
        ];
    }
}
