<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow;

use Qoqi\Flow\Plain;

/**
 * One of OutOfStock, Declined, Undeliverable.
 */
abstract class Problem
{
    public static function fromPlain(mixed $plain): self
    {
        [$variant, $values] = Plain::variant($plain);

        return match ($variant) {
            'OutOfStock' => new Problem\OutOfStock(Plain::string($values)),
            'Declined' => new Problem\Declined(Decline::fromPlain($values)),
            'Undeliverable' => new Problem\Undeliverable(Plain::string($values)),
            default => throw new \UnexpectedValueException(
                'a Problem is one of OutOfStock, Declined, Undeliverable, not ' . $variant,
            ),
        };
    }

    abstract public function toPlain(): mixed;
}
