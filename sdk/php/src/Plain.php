<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * Values in the form of the boundary, read as the types they are and
 * written back: what the code qoqi gen writes calls.
 */
final class Plain
{
    /**
     * @throws \UnexpectedValueException
     */
    public static function int(mixed $value): int
    {
        if (!is_int($value)) {
            throw new \UnexpectedValueException('an Int is a whole number, not ' . self::show($value));
        }

        return $value;
    }

    /**
     * A Float: a number, or "NaN", "+Inf", "-Inf".
     *
     * @throws \UnexpectedValueException
     */
    public static function float(mixed $value): float
    {
        return match (true) {
            is_int($value), is_float($value) => (float) $value,
            $value === 'NaN' => NAN,
            $value === '+Inf' => INF,
            $value === '-Inf' => -INF,
            default => throw new \UnexpectedValueException('a Float is a number, not ' . self::show($value)),
        };
    }

    public static function writeFloat(float $value): float|string
    {
        return match (true) {
            is_nan($value) => 'NaN',
            $value === INF => '+Inf',
            $value === -INF => '-Inf',
            default => $value,
        };
    }

    /**
     * @throws \UnexpectedValueException
     */
    public static function bool(mixed $value): bool
    {
        if (!is_bool($value)) {
            throw new \UnexpectedValueException('a Bool is true or false, not ' . self::show($value));
        }

        return $value;
    }

    /**
     * @throws \UnexpectedValueException
     */
    public static function string(mixed $value): string
    {
        if (!is_string($value)) {
            throw new \UnexpectedValueException('a String is a text, not ' . self::show($value));
        }

        return $value;
    }

    /**
     * An array: its items.
     *
     * @return list<mixed>
     * @throws \UnexpectedValueException
     */
    public static function list(mixed $value): array
    {
        if (!is_array($value) || !array_is_list($value)) {
            throw new \UnexpectedValueException('an array was expected, not ' . self::show($value));
        }

        return $value;
    }

    /**
     * An object: its fields, or a map's entries, by name.
     *
     * @return array<string, mixed>
     * @throws \UnexpectedValueException
     */
    public static function fields(mixed $value): array
    {
        if ($value instanceof \stdClass) {
            $value = (array) $value;
        }
        if (!is_array($value) || ($value !== [] && array_is_list($value))) {
            throw new \UnexpectedValueException('an object was expected, not ' . self::show($value));
        }
        /** @var array<string, mixed> $value */
        return $value;
    }

    /**
     * A variant of an enum: its name alone, "Unanswered", or an object of
     * one key, its name, to its values, {"Paid": "p-77"}.
     *
     * @return array{string, mixed}
     * @throws \UnexpectedValueException
     */
    public static function variant(mixed $value): array
    {
        if (is_string($value)) {
            return [$value, null];
        }
        $fields = self::fields($value);
        if (count($fields) !== 1) {
            throw new \UnexpectedValueException('a variant is its name, or an object of one key, its name');
        }

        return [(string) array_key_first($fields), $fields[array_key_first($fields)]];
    }

    /**
     * A Result, its value read by $ok and its error by $err.
     *
     * @template T
     * @template E
     * @param callable(mixed): T $ok
     * @param callable(mixed): E $err
     * @return Ok<T>|Err<E>
     * @throws \UnexpectedValueException
     */
    public static function result(mixed $value, callable $ok, callable $err): Ok|Err
    {
        [$variant, $values] = self::variant($value);

        return match ($variant) {
            'Ok' => new Ok($ok($values)),
            'Err' => new Err($err($values)),
            default => throw new \UnexpectedValueException('a Result is {"Ok": ...} or {"Err": ...}, not ' . $variant),
        };
    }

    private static function show(mixed $value): string
    {
        return json_encode($value, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE) ?: get_debug_type($value);
    }
}
