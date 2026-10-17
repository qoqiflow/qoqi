<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * An answer the daemon did not take: 410, the attempt was given up and the
 * step is to stop acting; 403, another token; 422, a value that does not
 * fit the step.
 */
final class AnswerRefused extends \RuntimeException
{
    public function __construct(public readonly int $status, string $said)
    {
        parent::__construct("the daemon answered {$status}: {$said}");
    }

    public function late(): bool
    {
        return $this->status === 410;
    }
}
