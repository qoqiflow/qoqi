<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow\Problem;

use App\Flow\Problem;

final class Undeliverable extends Problem
{
    public function __construct(
        public readonly string $value,
    ) {
    }

    public function toPlain(): mixed
    {
        return ['Undeliverable' => $this->value];
    }
}
