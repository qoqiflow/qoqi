<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow;

use Qoqi\Flow\Plain;

/**
 * Why a card is not charged.
 *
 * One of Insufficient, Fraud, Expired.
 */
abstract class Decline
{
    public static function fromPlain(mixed $plain): self
    {
        [$variant, $values] = Plain::variant($plain);

        return match ($variant) {
            'Insufficient' => new Decline\Insufficient(),
            'Fraud' => new Decline\Fraud(Plain::string($values)),
            'Expired' => new Decline\Expired(),
            default => throw new \UnexpectedValueException(
                'a Decline is one of Insufficient, Fraud, Expired, not ' . $variant,
            ),
        };
    }

    abstract public function toPlain(): mixed;
}
