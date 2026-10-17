<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * @template-covariant E
 * @implements Result<never, E>
 */
final class Err implements Result
{
    /**
     * @param E $error
     */
    public function __construct(public readonly mixed $error)
    {
    }

    public function toPlain(callable $ok, callable $err): array
    {
        return ['Err' => $err($this->error)];
    }
}
