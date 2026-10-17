<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow\Problem;

use App\Flow\Decline;
use App\Flow\Problem;

final class Declined extends Problem
{
    public function __construct(
        public readonly Decline $value,
    ) {
    }

    public function toPlain(): mixed
    {
        return ['Declined' => $this->value->toPlain()];
    }
}
