<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow\Decline;

use App\Flow\Decline;

final class Insufficient extends Decline
{
    public function toPlain(): mixed
    {
        return 'Insufficient';
    }
}
