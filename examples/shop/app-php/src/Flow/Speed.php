<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow;

use Qoqi\Flow\Plain;

/**
 * How fast a parcel goes: express in so many days.
 *
 * One of Standard, Express.
 */
abstract class Speed
{
    public static function fromPlain(mixed $plain): self
    {
        [$variant, $values] = Plain::variant($plain);

        return match ($variant) {
            'Standard' => new Speed\Standard(),
            'Express' => new Speed\Express(Plain::int($values)),
            default => throw new \UnexpectedValueException(
                'a Speed is one of Standard, Express, not ' . $variant,
            ),
        };
    }

    abstract public function toPlain(): mixed;
}
