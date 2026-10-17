<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * A step the daemon sent: its key, the same for every attempt and no other
 * step's, which goes to the world as an idempotency key; the attempt, from
 * 1; the token that answers for the attempt; when it is given up; how long
 * the daemon keeps an ended instance; the step's name and shape; and its
 * arguments, by name, in the form of the boundary.
 */
final class Task
{
    /**
     * @param array<string, mixed> $args
     */
    public function __construct(
        public readonly string $key,
        public readonly int $attempt,
        public readonly string $token,
        public readonly int $until,
        public readonly int $keep,
        public readonly string $step,
        public readonly string $shape,
        public readonly array $args,
        public readonly bool $probe = false,
        public readonly int $reclaim = 0,
    ) {
    }

    /**
     * Reads a step as the daemon sends it.
     *
     * @throws \JsonException
     * @throws \UnexpectedValueException
     */
    public static function fromJson(string $json): self
    {
        $fields = Plain::fields(json_decode($json, true, 512, JSON_THROW_ON_ERROR));

        return new self(
            key: Plain::string($fields['key'] ?? null),
            attempt: Plain::int($fields['attempt'] ?? null),
            token: Plain::string($fields['token'] ?? null),
            until: Plain::int($fields['until'] ?? null),
            keep: Plain::int($fields['keep'] ?? null),
            step: Plain::string($fields['step'] ?? null),
            shape: Plain::string($fields['shape'] ?? null),
            args: Plain::fields($fields['args'] ?? []),
            probe: ($fields['probe'] ?? false) === true,
            reclaim: is_int($fields['reclaim'] ?? null) ? $fields['reclaim'] : 0,
        );
    }

    /**
     * When the daemon gives the attempt up, in seconds since 1970.
     */
    public function deadline(): float
    {
        return $this->until / 1000;
    }
}
