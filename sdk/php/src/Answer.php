<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * What the application answers a step, as the daemon reads it: a value,
 * with its undo for a step that names one; or a retry, a failure,
 * unknown, or under way.
 */
final class Answer
{
    /**
     * @param array<string, mixed> $body
     */
    private function __construct(
        public readonly array $body,
        public readonly int $status = 200,
    ) {
    }

    /**
     * What the step returns, in the form of the boundary.
     */
    public static function value(mixed $plain): self
    {
        return new self(['value' => $plain]);
    }

    /**
     * What the step returns and the argument of the step that undoes it,
     * which its declaration names: given with every value that is not an
     * Err.
     */
    public static function withUndo(mixed $plain, mixed $undo): self
    {
        return new self(['value' => $plain, 'undo' => $undo]);
    }

    /**
     * The step's body went wrong.
     */
    public static function failed(string $why): self
    {
        return new self(['failed' => $why]);
    }

    /**
     * Not done: sent again after so many milliseconds, as its next attempt.
     */
    public static function retry(string $reason, int $after): self
    {
        return new self(['retry' => ['reason' => $reason, 'after' => $after]]);
    }

    /**
     * Acted, and nobody can say how.
     */
    public static function unknown(): self
    {
        return new self(['unknown' => true]);
    }

    /**
     * The answer comes later, on a request of its own (Daemon::complete).
     */
    public static function later(): self
    {
        return new self(['working' => true], 202);
    }

    /**
     * No handler for a step of this shape: nothing was done.
     */
    public static function unknownShape(string $shape): self
    {
        return new self(['unknown_shape' => $shape]);
    }

    /**
     * Unseen: a probe of a key under which nothing was done.
     */
    public static function unseen(): self
    {
        return new self(['unseen' => true]);
    }

    /**
     * An attempt before acts under the key still: asked again later.
     */
    public static function underWay(): self
    {
        return new self(['working' => true]);
    }

    /**
     * An answer kept, as it was written.
     *
     * @throws \JsonException
     * @throws \UnexpectedValueException
     */
    public static function fromJson(string $json): self
    {
        return new self(Plain::fields(json_decode($json, true, 512, JSON_THROW_ON_ERROR)));
    }

    /**
     * Whether it is a value, which a Keeper keeps: a retry or a failure
     * acts again next time.
     */
    public function isValue(): bool
    {
        return array_key_exists('value', $this->body);
    }

    /**
     * The answer a step's exception gives.
     */
    public static function fromThrowable(\Throwable $e): self
    {
        return match (true) {
            $e instanceof Retry => self::retry($e->getMessage(), $e->after),
            $e instanceof Unknown => self::unknown(),
            $e instanceof Later => self::later(),
            default => self::failed($e::class . ': ' . $e->getMessage()),
        };
    }
}
