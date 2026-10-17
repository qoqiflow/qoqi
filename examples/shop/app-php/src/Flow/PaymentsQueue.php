<?php

/**
 * Written by qoqi gen from orders.qi: change the flow, not this file.
 */

declare(strict_types=1);

namespace App\Flow;

use Qoqi\Flow\Answer;
use Qoqi\Flow\Plain;
use Qoqi\Flow\Queue;
use Qoqi\Flow\Task;
use Qoqi\Flow\Undone;

/**
 * Makes the handler of the queue payments, at the address qoqi.json gives it.
 */
final class PaymentsQueue
{
    /**
     * The shapes of the steps of payments: which handler answers which step.
     */
    public const SHAPES = [
        'charge' => '999298825e404c21',
        'refund' => '6ac84f977b4139fe',
    ];

    public static function make(Payments $steps, string ...$secrets): Queue
    {
        $queue = new Queue('payments', ...$secrets);

        $queue->handle(self::SHAPES['charge'], static function (Task $task) use ($steps): Answer {
            $args = $task->args;
            $result = $steps->charge(
                Plain::string($args['customer'] ?? null),
                Plain::int($args['cents'] ?? null),
                $task,
            );
            if ($result instanceof Undone) {
                return Answer::withUndo(
                    ['Ok' => $result->value],
                    $result->undo,
                );
            }

            return Answer::value(['Err' => $result->error->toPlain()]);
        });

        $queue->handle(self::SHAPES['refund'], static function (Task $task) use ($steps): Answer {
            $args = $task->args;
            $result = $steps->refund(
                Plain::string($args['charge'] ?? null),
                $task,
            );

            return Answer::value($result->toPlain(
                static fn (null $v): mixed => null,
                static fn (Decline $v): mixed => $v->toPlain(),
            ));
        });

        return $queue;
    }
}
