<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * Qoqi's Result<T, E>: Ok with a T, or Err with an E, written at the
 * boundary {"Ok": ...} or {"Err": ...}. An Err is an answer, not a
 * failure: the flow decides on it.
 *
 * @template-covariant T
 * @template-covariant E
 */
interface Result
{
    /**
     * The Result in the form of the boundary, its value written by $ok or
     * its error by $err.
     *
     * @param callable(T): mixed $ok
     * @param callable(E): mixed $err
     * @return array<string, mixed>
     */
    public function toPlain(callable $ok, callable $err): array;
}
