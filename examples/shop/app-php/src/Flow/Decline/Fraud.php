<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow\Decline;

use App\Flow\Decline;

final class Fraud extends Decline
{
    public function __construct(
        public readonly string $value,
    ) {
    }

    public function toPlain(): mixed
    {
        return ['Fraud' => $this->value];
    }
}
