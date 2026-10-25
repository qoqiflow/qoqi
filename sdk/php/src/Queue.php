<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * Serves the steps of one queue, at the address qoqi.json gives it: it
 * checks each request's signature, reads the step, and answers it with
 * the handler of its shape; a GET is answered the manifest, which says
 * which shapes it knows.
 */
final class Queue
{
    /** @var list<string> */
    private array $secrets;

    /** @var array<string, \Closure(Task): Answer> */
    private array $steps = [];

    /** @var array<string, Keeper> */
    private array $once = [];

    /** @var array<string, Keeper> */
    private array $started = [];

    /** What keeps the steps not named, every step when only one is said. */
    private ?Keeper $onceAll = null;

    private ?Keeper $startedAll = null;

    public function __construct(public readonly string $name, string ...$secrets)
    {
        $this->secrets = array_values(array_filter($secrets, fn (string $s): bool => $s !== ''));
        if ($this->secrets === []) {
            // getenv() of a variable not set gives false, (string) false
            // nothing: a queue whose secret is nothing would take a
            // request anyone signs
            throw new \InvalidArgumentException("the queue {$name} has no secret: give it the one of its qoqi.json");
        }
    }

    /**
     * Says what carries out the steps of a shape: the shape names a step
     * and its signature, so a step whose signature changed has another.
     *
     * @param \Closure(Task): Answer $step
     */
    public function handle(string $shape, \Closure $step): void
    {
        $this->steps[$shape] = $step;
    }

    /**
     * Keeps the answers of the queue's steps under once: their effect is
     * in the application's database, which they write with the PDO of the
     * Keeper, in the transaction it holds with their answer. Without
     * shapes, every step of the queue; with them, those.
     */
    public function keepOnce(Keeper $keeper, string ...$shapes): self
    {
        if ($shapes === []) {
            $this->onceAll = $keeper;
        }
        foreach ($shapes as $shape) {
            $this->once[$shape] = $keeper;
            unset($this->started[$shape]);
        }

        return $this;
    }

    /**
     * Keeps the answers of the queue's steps under started: their effect
     * is elsewhere, a claim written before they act. Without shapes, every
     * step of the queue; with them, those.
     */
    public function keepStarted(Keeper $keeper, string ...$shapes): self
    {
        if ($shapes === []) {
            $this->startedAll = $keeper;
        }
        foreach ($shapes as $shape) {
            $this->started[$shape] = $keeper;
            unset($this->once[$shape]);
        }

        return $this;
    }

    /**
     * How a shape's answers are kept: under once, under started, or
     * neither.
     *
     * @return array{Keeper|null, Keeper|null}
     */
    private function keeping(string $shape): array
    {
        return match (true) {
            isset($this->once[$shape]) => [$this->once[$shape], null],
            isset($this->started[$shape]) => [null, $this->started[$shape]],
            !isset($this->steps[$shape]) => [null, null],
            $this->onceAll !== null => [$this->onceAll, null],
            default => [null, $this->startedAll],
        };
    }

    /**
     * The shapes kept one way, sorted.
     *
     * @return list<string>
     */
    private function kept(bool $once): array
    {
        $shapes = [];
        foreach ($this->shapes() as $shape) {
            [$o, $s] = $this->keeping($shape);
            if ($once ? $o !== null : $s !== null) {
                $shapes[] = $shape;
            }
        }

        return $shapes;
    }

    /**
     * @return list<string>
     */
    public function shapes(): array
    {
        $shapes = array_keys($this->steps);
        sort($shapes);

        return $shapes;
    }

    /**
     * Answers the request this process serves, from PHP's own globals.
     */
    public function serve(): void
    {
        $method = self::server('REQUEST_METHOD', 'GET');
        $path = parse_url(self::server('REQUEST_URI', '/'), PHP_URL_PATH) ?: '/';
        $header = self::server('HTTP_X_FLOW_SIGNATURE', '');
        $body = (string) file_get_contents('php://input');

        [$status, $said] = $this->respond($method, $path, $header, $body, (int) (microtime(true) * 1000));
        http_response_code($status);
        header('Content-Type: application/json');
        echo $said;
    }

    /**
     * What a request is answered: its status and its body.
     *
     * @return array{int, string}
     */
    public function respond(string $method, string $path, string $header, string $body, int $now): array
    {
        try {
            Signature::verify($header, $now, $method, $path, $this->name, $body, $this->secrets);
        } catch (SignatureError $e) {
            return [401, self::json(['error' => 'signature', 'message' => $e->getMessage()])];
        }
        if ($method === 'GET') {
            return [200, self::json([
                'shapes' => $this->shapes(),
                'once' => $this->kept(true),
                'started' => $this->kept(false),
            ])];
        }
        if ($method !== 'POST') {
            return [405, self::json(['error' => 'method'])];
        }
        try {
            $task = Task::fromJson($body);
        } catch (\JsonException | \UnexpectedValueException $e) {
            return [400, self::json(['error' => 'step', 'message' => $e->getMessage()])];
        }
        [$once, $started] = $this->keeping($task->shape);
        $keeper = $started ?? $once;
        if ($task->probe) {
            $answer = $keeper === null ? Answer::unseen() : $keeper->probe($task);

            return [200, self::json($answer->body)];
        }
        $step = $this->steps[$task->shape] ?? null;
        $answer = match (true) {
            $step === null => Answer::unknownShape($task->shape),
            $once !== null => $once->once($task, static fn (Task $t): Answer => self::carryOut($step, $t)),
            $started !== null => $started->started($task, static fn (Task $t): Answer => self::carryOut($step, $t)),
            default => self::carryOut($step, $task),
        };

        return [$answer->status, $answer->status === 202 ? '' : self::json($answer->body)];
    }

    private static function server(string $name, string $otherwise): string
    {
        $value = $_SERVER[$name] ?? null;

        return is_string($value) ? $value : $otherwise;
    }

    /**
     * @param \Closure(Task): Answer $step
     */
    private static function carryOut(\Closure $step, Task $task): Answer
    {
        try {
            return $step($task);
        } catch (\Throwable $e) {
            return Answer::fromThrowable($e);
        }
    }

    /**
     * @param array<string, mixed> $value
     */
    public static function json(array $value): string
    {
        return json_encode(
            $value,
            JSON_THROW_ON_ERROR | JSON_PRESERVE_ZERO_FRACTION | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE,
        );
    }
}
