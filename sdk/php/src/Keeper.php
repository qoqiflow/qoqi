<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * Keeps the answers of steps in the application's own database, keyed by
 * the step's key, so that a step sent again under its key does not act
 * twice. Two kinds, as the queue says of each shape (Queue::keepOnce,
 * Queue::keepStarted):
 *
 * - once, for an effect in that database: the effect and the answer are
 *   written in one transaction of the Keeper's PDO, with which the step
 *   writes. Sent again, it gives the answer kept, without acting;
 * - started, for an effect elsewhere: a claim is written before it acts,
 *   alive until the attempt's deadline, then the answer. A claim found
 *   past its deadline without its answer is a process that died as it
 *   acted: unknown, until a person says what happened.
 */
final class Keeper
{
    private const ANSWERED = 'answered';
    private const CLAIMED = 'started';

    /** The key column, quoted for the database. */
    private readonly string $key;

    public function __construct(
        private readonly \PDO $db,
        private readonly string $table = 'flow_steps',
    ) {
        // key is a reserved word of MySQL: the column is quoted as each
        // database quotes a name
        $this->key = $db->getAttribute(\PDO::ATTR_DRIVER_NAME) === 'mysql' ? '`key`' : '"key"';
    }

    /**
     * Makes the table of answers, if it is not there.
     */
    public function setup(): void
    {
        $this->db->exec("CREATE TABLE IF NOT EXISTS {$this->table} (
            {$this->key} VARCHAR(255) PRIMARY KEY,
            state VARCHAR(16) NOT NULL,
            answer TEXT NOT NULL,
            beat BIGINT NOT NULL
        )");
    }

    /**
     * @param \Closure(Task): Answer $step
     */
    public function once(Task $task, \Closure $step): Answer
    {
        $kept = $this->kept($task->key);
        if ($kept !== null) {
            return $kept;
        }
        $this->db->beginTransaction();
        $answer = $step($task);
        if (!$answer->isValue()) {
            // Only values are kept; another delivery of it that won
            // meanwhile, its effect clashing with this one's, gives its own
            $this->db->rollBack();

            return $this->kept($task->key) ?? $answer;
        }
        try {
            $this->db->prepare("INSERT INTO {$this->table} ({$this->key}, state, answer, beat) VALUES (?, ?, ?, ?)")
                ->execute([$task->key, self::ANSWERED, Queue::json($answer->body), self::now()]);
            $this->db->commit();
        } catch (\PDOException $e) {
            if ($this->db->inTransaction()) {
                $this->db->rollBack();
            }

            return $this->kept($task->key) ?? Answer::failed($e->getMessage());
        }

        return $answer;
    }

    /**
     * @param \Closure(Task): Answer $step
     */
    public function started(Task $task, \Closure $step): Answer
    {
        $row = $this->row($task->key);
        if ($row !== null && $row['state'] === self::ANSWERED) {
            return Answer::fromJson($row['answer']);
        }
        if ($row !== null && self::now() < $row['beat']) {
            return Answer::underWay();
        }
        if ($row !== null && $task->reclaim === 0) {
            return Answer::unknown();
        }
        if ($row !== null) {
            // A person said the dead claim did nothing: it is dropped
            $this->db->prepare("DELETE FROM {$this->table} WHERE {$this->key} = ? AND state = ?")
                ->execute([$task->key, self::CLAIMED]);
        }
        try {
            $this->db->prepare("INSERT INTO {$this->table} ({$this->key}, state, answer, beat) VALUES (?, ?, '', ?)")
                ->execute([$task->key, self::CLAIMED, $task->until]);
        } catch (\PDOException) {
            // The loser of a race for the claim does not act
            return Answer::underWay();
        }
        $answer = $step($task);
        if (!$answer->isValue()) {
            $this->db->prepare("DELETE FROM {$this->table} WHERE {$this->key} = ? AND state = ?")
                ->execute([$task->key, self::CLAIMED]);

            return $answer;
        }
        $this->db->prepare("UPDATE {$this->table} SET state = ?, answer = ? WHERE {$this->key} = ?")
            ->execute([self::ANSWERED, Queue::json($answer->body), $task->key]);

        return $answer;
    }

    /**
     * What became of an attempt under its key: its answer, unseen, under
     * way, or unknown.
     */
    public function probe(Task $task): Answer
    {
        $row = $this->row($task->key);

        return match (true) {
            $row === null => Answer::unseen(),
            $row['state'] === self::ANSWERED => Answer::fromJson($row['answer']),
            self::now() < $row['beat'] => Answer::underWay(),
            default => Answer::unknown(),
        };
    }

    /**
     * Drops what is under a key: a person said what happened.
     */
    public function forget(string $key): void
    {
        $this->db->prepare("DELETE FROM {$this->table} WHERE {$this->key} = ?")->execute([$key]);
    }

    /**
     * Drops the answers older than the daemon keeps an ended instance.
     *
     * @param int $keep milliseconds, as a task's keep says
     */
    public function sweep(int $keep): void
    {
        $this->db->prepare("DELETE FROM {$this->table} WHERE state = ? AND beat < ?")
            ->execute([self::ANSWERED, self::now() - $keep]);
    }

    private function kept(string $key): ?Answer
    {
        $row = $this->row($key);

        return $row !== null && $row['state'] === self::ANSWERED ? Answer::fromJson($row['answer']) : null;
    }

    /**
     * @return array{state: string, answer: string, beat: int}|null
     */
    private function row(string $key): ?array
    {
        $statement = $this->db->prepare("SELECT state, answer, beat FROM {$this->table} WHERE {$this->key} = ?");
        $statement->execute([$key]);
        $row = $statement->fetch(\PDO::FETCH_ASSOC);
        if (!is_array($row)) {
            return null;
        }

        return [
            'state' => is_string($row['state'] ?? null) ? $row['state'] : '',
            'answer' => is_string($row['answer'] ?? null) ? $row['answer'] : '',
            'beat' => is_numeric($row['beat'] ?? null) ? (int) $row['beat'] : 0,
        ];
    }

    private static function now(): int
    {
        return (int) (microtime(true) * 1000);
    }
}
