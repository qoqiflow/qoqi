<?php

/**
 * The page the daemon sends the shop's steps to: /warehouse and
 * /payments, the queues qoqi.json declares.
 *
 *     SHOP_SECRET=dev php -S 127.0.0.1:8090 -t public public/index.php
 */

declare(strict_types=1);

require __DIR__ . '/../bootstrap.php';

use App\Flow\PaymentsQueue;
use App\Flow\WarehouseQueue;
use App\Shop\Database;
use App\Shop\Payments;
use App\Shop\Warehouse;
use Qoqi\Flow\Keeper;

$secret = getenv('SHOP_SECRET');
if (!is_string($secret) || $secret === '') {
    http_response_code(500);
    echo 'SHOP_SECRET is the secret the daemon signs the steps with, as qoqi.json says';

    return;
}

$db = Database::open(getenv('SHOP_DB') ?: __DIR__ . '/../var/shop.sqlite');
$keeper = new Keeper($db);
$keeper->setup();

$uri = $_SERVER['REQUEST_URI'] ?? '/';
// The warehouse's steps change the shop's database: under once. A charge
// reaches the provider: under started
$queue = match (parse_url(is_string($uri) ? $uri : '/', PHP_URL_PATH)) {
    '/warehouse' => WarehouseQueue::make(new Warehouse($db), $secret)->keepOnce($keeper),
    '/payments' => PaymentsQueue::make(new Payments($db), $secret)->keepStarted($keeper),
    default => null,
};

if ($queue === null) {
    http_response_code(404);

    return;
}

$queue->serve();
