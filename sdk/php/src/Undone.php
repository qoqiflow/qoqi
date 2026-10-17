<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * What a step with an undo returns when it did its work: Ok with its
 * value, and the argument of the step that undoes it.
 *
 * @template-covariant T
 * @template-covariant U
 */
final class Undone
{
    /**
     * @param T $value
     * @param U $undo
     */
    public function __construct(
        public readonly mixed $value,
        public readonly mixed $undo,
    ) {
    }
}
