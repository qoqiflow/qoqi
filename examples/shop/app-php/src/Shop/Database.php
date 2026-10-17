<?php

declare(strict_types=1);

namespace App\Shop;

/**
 * The shop's database: its stock, what is held and shipped, its charges,
 * and the answers of its steps, which qoqi/flow's Keeper keeps beside
 * them: a step sent twice acts once.
 */
final class Database
{
    public static function open(string $path): \PDO
    {
        if (!is_dir(dirname($path))) {
            mkdir(dirname($path), 0o755, true);
        }
        $db = new \PDO('sqlite:' . $path, options: [\PDO::ATTR_ERRMODE => \PDO::ERRMODE_EXCEPTION]);
        $db->exec('PRAGMA busy_timeout = 5000');
        $db->exec(
            'CREATE TABLE IF NOT EXISTS stock (sku TEXT PRIMARY KEY, quantity INTEGER NOT NULL);
             CREATE TABLE IF NOT EXISTS holds (hold TEXT PRIMARY KEY, items TEXT NOT NULL);
             CREATE TABLE IF NOT EXISTS parcels (parcel TEXT PRIMARY KEY, hold TEXT NOT NULL);
             CREATE TABLE IF NOT EXISTS charges (charge TEXT PRIMARY KEY, customer TEXT NOT NULL);
             INSERT OR IGNORE INTO stock (sku, quantity) VALUES (\'mug\', 10), (\'lamp\', 1);',
        );

        return $db;
    }

    /**
     * The first column of the first row a query gives, or null.
     *
     * @param list<string|int> $params
     */
    public static function one(\PDO $db, string $query, array $params): mixed
    {
        $statement = $db->prepare($query);
        $statement->execute($params);
        $value = $statement->fetchColumn();

        return $value === false ? null : $value;
    }

    public static function say(string $line): void
    {
        file_put_contents('php://stderr', $line . "\n");
    }
}
