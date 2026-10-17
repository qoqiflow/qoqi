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
 * Makes the handler of the queue warehouse, at the address qoqi.json gives it.
 */
final class WarehouseQueue
{
    /**
     * The shapes of the steps of warehouse: which handler answers which step.
     */
    public const SHAPES = [
        'release' => '2b90f4d358ae69fa',
        'reserve' => 'f2aac7a6deff1975',
        'ship' => '03f2e357d2b665db',
    ];

    public static function make(Warehouse $steps, string ...$secrets): Queue
    {
        $queue = new Queue('warehouse', ...$secrets);

        $queue->handle(self::SHAPES['release'], static function (Task $task) use ($steps): Answer {
            $args = $task->args;
            $result = $steps->release(
                Hold::fromPlain($args['hold'] ?? null),
                $task,
            );

            return Answer::value($result->toPlain(
                static fn (null $v): mixed => null,
                static fn (Problem $v): mixed => $v->toPlain(),
            ));
        });

        $queue->handle(self::SHAPES['reserve'], static function (Task $task) use ($steps): Answer {
            $args = $task->args;
            $result = $steps->reserve(
                Plain::string($args['order'] ?? null),
                array_map(static fn (mixed $v): Item => Item::fromPlain($v), Plain::list($args['items'] ?? null)),
                $task,
            );
            if ($result instanceof Undone) {
                return Answer::withUndo(
                    ['Ok' => $result->value->toPlain()],
                    $result->undo->toPlain(),
                );
            }

            return Answer::value(['Err' => $result->error->toPlain()]);
        });

        $queue->handle(self::SHAPES['ship'], static function (Task $task) use ($steps): Answer {
            $args = $task->args;
            $result = $steps->ship(
                Hold::fromPlain($args['hold'] ?? null),
                Plain::string($args['to'] ?? null),
                Speed::fromPlain($args['speed'] ?? null),
                $task,
            );

            return Answer::value($result->toPlain(
                static fn (string $v): mixed => $v,
                static fn (Problem $v): mixed => $v->toPlain(),
            ));
        });

        return $queue;
    }
}
