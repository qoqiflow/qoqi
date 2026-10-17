<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow\Speed;

use App\Flow\Speed;

final class Express extends Speed
{
    public function __construct(
        public readonly int $value,
    ) {
    }

    public function toPlain(): mixed
    {
        return ['Express' => $this->value];
    }
}
