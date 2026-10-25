<?php

// Runs a Keeper on SQLite through PDO: php sdk/php/tests/KeeperTest.php.
// It exits non-zero when anything is not as it should be.

declare(strict_types=1);

use Qoqi\Flow\Answer;
use Qoqi\Flow\Keeper;
use Qoqi\Flow\Task;

spl_autoload_register(function (string $class): void {
    $prefix = 'Qoqi\\Flow\\';
    if (str_starts_with($class, $prefix)) {
        require __DIR__ . '/../src/' . str_replace('\\', '/', substr($class, strlen($prefix))) . '.php';
    }
});

/**
 * A PDO on SQLite that keeps the queries it is given, and may say it is
 * MySQL's: SQLite takes a name quoted with backticks as MySQL does, so a
 * Keeper that believes it is on MySQL runs on it all the same.
 */
final class Recorded extends \PDO
{
    /** @var list<string> */
    public array $said = [];

    public function __construct(private readonly ?string $driver = null)
    {
        parent::__construct('sqlite::memory:');
        $this->setAttribute(\PDO::ATTR_ERRMODE, \PDO::ERRMODE_EXCEPTION);
    }

    public function getAttribute(int $attribute): mixed
    {
        if ($attribute === \PDO::ATTR_DRIVER_NAME && $this->driver !== null) {
            return $this->driver;
        }

        return parent::getAttribute($attribute);
    }

    public function exec(string $statement): int|false
    {
        $this->said[] = $statement;

        return parent::exec($statement);
    }

    public function prepare(string $query, array $options = []): \PDOStatement|false
    {
        $this->said[] = $query;

        return parent::prepare($query, $options);
    }
}

$failures = 0;

function check(bool $ok, string $what): void
{
    global $failures;
    if (!$ok) {
        $failures++;
        fwrite(STDERR, "FAIL: {$what}\n");
    }
}

function task(string $key, bool $probe = false, int $until = 0, int $reclaim = 0): Task
{
    $until = $until !== 0 ? $until : (int) (microtime(true) * 1000) + 60_000;

    return new Task($key, 1, 't', $until, 0, 'ship', 'ship-1', [], $probe, $reclaim);
}

foreach (['sqlite' => ['"key"', '`key`'], 'mysql' => ['`key`', '"key"']] as $driver => [$quoted, $other]) {
    $db = new Recorded($driver === 'mysql' ? 'mysql' : null);
    $db->exec('CREATE TABLE shipped (id TEXT PRIMARY KEY)');
    $keeper = new Keeper($db);
    $keeper->setup();
    $keeper->setup();

    // Under once: the effect and the answer in one transaction, kept for
    // the next delivery, which does not act
    $acted = 0;
    $ship = function (Task $task) use ($db, &$acted): Answer {
        $acted++;
        $db->prepare('INSERT INTO shipped (id) VALUES (?)')->execute([$task->key]);

        return Answer::value(['Ok' => "parcel-{$task->key}"]);
    };
    check($keeper->once(task('a'), $ship)->body === ['value' => ['Ok' => 'parcel-a']], "{$driver}: once answers");
    check($keeper->once(task('a'), $ship)->body === ['value' => ['Ok' => 'parcel-a']], "{$driver}: once, sent again, answers what it did");
    check($acted === 1, "{$driver}: once acts once, acted {$acted}");

    // A failure under once leaves neither its effect nor an answer
    $keeper->once(task('b'), function (Task $task) use ($db): Answer {
        $db->prepare('INSERT INTO shipped (id) VALUES (?)')->execute([$task->key]);

        return Answer::failed('the carrier refused');
    });
    check((int) $db->query("SELECT COUNT(*) FROM shipped WHERE id = 'b'")->fetchColumn() === 0, "{$driver}: a failure under once leaves no effect");
    check($keeper->probe(task('b', probe: true))->body === ['unseen' => true], "{$driver}: a failure under once keeps no answer");

    // Under started: a claim, then the answer, which a probe gives
    $seen = null;
    $answer = $keeper->started(task('w'), function (Task $task) use ($keeper, &$seen): Answer {
        $seen = $keeper->probe(task('w', probe: true))->body;

        return Answer::value(['Ok' => 'w-1']);
    });
    check($answer->body === ['value' => ['Ok' => 'w-1']], "{$driver}: started answers");
    check($seen === ['working' => true], "{$driver}: a probe while it acts says it is under way");
    check($keeper->probe(task('w', probe: true))->body === ['value' => ['Ok' => 'w-1']], "{$driver}: a probe after gives its answer");
    check($keeper->probe(task('never', probe: true))->body === ['unseen' => true], "{$driver}: a probe of what was never seen");

    // A process that died as it acted: unknown, until a person says it did
    // nothing; then the next delivery acts. Its claim stays, past its
    // deadline, as the process it was in did not live to drop it
    $dead = fn (): Answer => Answer::value(['Ok' => 'w-dead']);
    try {
        $keeper->started(task('dead', until: 1), function (): Answer {
            throw new \RuntimeException('it died');
        });
    } catch (\RuntimeException) {
    }
    check($keeper->probe(task('dead', probe: true))->body === ['unknown' => true], "{$driver}: a dead claim is unknown");
    check($keeper->started(task('dead'), $dead)->body === ['unknown' => true], "{$driver}: started on a dead claim is unknown");
    check($keeper->started(task('dead', reclaim: 1), $dead)->body === ['value' => ['Ok' => 'w-dead']], "{$driver}: reclaimed, it acts");

    // A step that answers later keeps its claim: sent again, or probed,
    // before its answer, it is under way, and does not act a second time;
    // its answer, kept by the Keeper, is what it gives after
    $acts = 0;
    $slow = function () use (&$acts): Answer {
        $acts++;

        return Answer::later();
    };
    check($keeper->started(task('slow'), $slow)->isLater(), "{$driver}: a later answer is later");
    check($keeper->probe(task('slow', probe: true))->body === ['working' => true], "{$driver}: probed before its answer, it is under way");
    check($keeper->started(task('slow'), $slow)->body === ['working' => true], "{$driver}: sent again before its answer, it is under way");
    check($acts === 1, "{$driver}: a step that answers later acts once, acted {$acts}");
    $keeper->answered(task('slow'), Answer::value(['Ok' => 'slow-1']));
    check($keeper->started(task('slow'), $slow)->body === ['value' => ['Ok' => 'slow-1']], "{$driver}: sent again after, it gives its answer");

    $keeper->forget('a');
    check($keeper->probe(task('a', probe: true))->body === ['unseen' => true], "{$driver}: forgotten");
    // An answer under started keeps its claim's deadline, a minute ahead
    $keeper->sweep(-120_000);
    check($keeper->probe(task('w', probe: true))->body === ['unseen' => true], "{$driver}: swept");

    // Every query of the Keeper's quotes the key column as its database does
    foreach ($db->said as $query) {
        if (str_contains($query, 'flow_steps') && str_contains($query, 'key')) {
            check(str_contains($query, $quoted) && !str_contains($query, $other)
                && preg_match('/[^"`]key[^"`]/', $query) !== 1, "{$driver}: the key column is quoted {$quoted}: {$query}");
        }
    }
}

// A secret of nothing is one anyone has: a queue is not made with one,
// and a request signed with one is refused
try {
    new \Qoqi\Flow\Queue('billing', (string) getenv('QOQI_NO_SUCH_SECRET'));
    check(false, 'a queue made with a secret of nothing');
} catch (\InvalidArgumentException) {
}
$now = (int) (microtime(true) * 1000);
$forged = "t={$now}, v1=" . hash_hmac('sha256', "{$now}\nPOST\n/q\nbilling\n{}", '');
try {
    \Qoqi\Flow\Signature::verify($forged, $now, 'POST', '/q', 'billing', '{}', ['', 'the real one']);
    check(false, 'a request signed with a secret of nothing');
} catch (\Qoqi\Flow\SignatureError) {
}

if ($failures > 0) {
    fwrite(STDERR, "{$failures} failed\n");
    exit(1);
}
echo "ok\n";
