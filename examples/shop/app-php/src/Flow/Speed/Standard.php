<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow\Speed;

use App\Flow\Speed;

final class Standard extends Speed
{
    public function toPlain(): mixed
    {
        return 'Standard';
    }
}
