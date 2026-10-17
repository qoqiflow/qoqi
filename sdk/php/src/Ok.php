<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * @template-covariant T
 * @implements Result<T, never>
 */
final class Ok implements Result
{
    /**
     * @param T $value
     */
    public function __construct(public readonly mixed $value = null)
    {
    }

    public function toPlain(callable $ok, callable $err): array
    {
        return ['Ok' => $ok($this->value)];
    }
}
