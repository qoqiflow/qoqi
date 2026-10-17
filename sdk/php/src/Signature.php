<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * The signature of every request the daemon sends a queue: the moment,
 * the method, the path, the queue, then the body, each on a line of its
 * own, with the queue's secret, as X-Flow-Signature writes it:
 * "t=<ms>, v1=<hex>".
 */
final class Signature
{
    public const HEADER = 'X-Flow-Signature';

    /** How far from this clock a signature's moment may be, in milliseconds. */
    public const SKEW = 300_000;

    public static function sign(
        string $secret,
        int $at,
        string $method,
        string $path,
        string $queue,
        string $body,
    ): string {
        return sprintf('t=%d, v1=%s', $at, self::mac($secret, $at, $method, $path, $queue, $body));
    }

    /**
     * Checks a signature with any of the queue's secrets: while one is
     * being changed, the daemon signs with both.
     *
     * @param list<string> $secrets
     * @throws SignatureError
     */
    public static function verify(
        string $header,
        int $now,
        string $method,
        string $path,
        string $queue,
        string $body,
        array $secrets,
    ): void {
        $at = null;
        $given = [];
        foreach (explode(',', $header) as $part) {
            [$key, $value] = array_pad(explode('=', trim($part), 2), 2, '');
            if ($key === 't' && ctype_digit($value)) {
                $at = (int) $value;
            } elseif ($key === 'v1') {
                $given[] = $value;
            }
        }
        if ($at === null || $given === []) {
            throw new SignatureError(self::HEADER . ' is missing, or not t=..., v1=...');
        }
        if (abs($now - $at) > self::SKEW) {
            throw new SignatureError('the request was signed too far from this clock');
        }
        foreach ($secrets as $secret) {
            $want = self::mac($secret, $at, $method, $path, $queue, $body);
            foreach ($given as $signature) {
                if (hash_equals($want, $signature)) {
                    return;
                }
            }
        }

        throw new SignatureError('the request is not signed by the daemon');
    }

    private static function mac(
        string $secret,
        int $at,
        string $method,
        string $path,
        string $queue,
        string $body,
    ): string {
        return hash_hmac('sha256', "{$at}\n{$method}\n{$path}\n{$queue}\n{$body}", $secret);
    }
}
