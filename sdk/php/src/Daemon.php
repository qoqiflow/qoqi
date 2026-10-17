<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * Where the daemon answers, for the answers that come later: a step that
 * threw Later gives its answer here once its work is done, before the
 * task's deadline.
 */
final class Daemon
{
    public function __construct(public readonly string $url)
    {
    }

    /**
     * Gives the answer of a step: whether the daemon had one already, the
     * first standing.
     *
     * @throws AnswerRefused
     */
    public function complete(Task $task, Answer $answer): bool
    {
        [$status, $said] = $this->post($task, $answer->body);
        if ($status === 200) {
            $fields = json_decode($said, true);

            return is_array($fields) && ($fields['already'] ?? false) === true;
        }

        throw new AnswerRefused($status, $said);
    }

    /**
     * Asks whether the attempt is still under way: AnswerRefused, 410,
     * once the daemon gave it up, when a step that works for long stops.
     *
     * @throws AnswerRefused
     */
    public function working(Task $task): void
    {
        [$status, $said] = $this->post($task, ['working' => true]);
        if ($status !== 200) {
            throw new AnswerRefused($status, $said);
        }
    }

    /**
     * @param array<string, mixed> $answer
     * @return array{int, string}
     */
    private function post(Task $task, array $answer): array
    {
        $body = Queue::json(['key' => $task->key, 'attempt' => $task->attempt, 'token' => $task->token] + $answer);
        $context = stream_context_create(['http' => [
            'method' => 'POST',
            'header' => "Content-Type: application/json\r\n",
            'content' => $body,
            'ignore_errors' => true,
            'timeout' => 30,
        ]]);
        $said = @file_get_contents(rtrim($this->url, '/') . '/api/attempts', false, $context);
        if ($said === false) {
            throw new AnswerRefused(0, 'the daemon could not be reached at ' . $this->url);
        }
        $status = 0;
        foreach (http_get_last_response_headers() ?? [] as $line) {
            if (preg_match('#^HTTP/\S+ (\d{3})#', $line, $m) === 1) {
                $status = (int) $m[1];
            }
        }

        return [$status, $said];
    }
}
