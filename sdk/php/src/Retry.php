<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * Thrown by a step that did nothing, to be sent again after a while, as
 * its next attempt: a gateway down for a moment.
 */
final class Retry extends \RuntimeException
{
    /**
     * @param int $after milliseconds
     */
    public function __construct(string $reason, public readonly int $after = 1000)
    {
        parent::__construct($reason);
    }
}
