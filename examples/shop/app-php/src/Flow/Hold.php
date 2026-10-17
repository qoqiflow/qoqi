<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow;

use Qoqi\Flow\Plain;

/**
 * What the warehouse holds for an order until it ships.
 */
final class Hold
{
    public function __construct(
        public readonly string $id,
    ) {
    }

    public static function fromPlain(mixed $plain): self
    {
        $fields = Plain::fields($plain);

        return new self(
            id: Plain::string($fields['id'] ?? null),
        );
    }

    public function toPlain(): object
    {
        return (object) [
            'id' => $this->id,
        ];
    }
}
