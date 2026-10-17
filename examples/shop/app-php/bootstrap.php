<?php

/**
 * Loads the classes: through Composer once it has installed them, or
 * from the folders themselves, so that the example runs as it is.
 */

declare(strict_types=1);

if (is_file(__DIR__ . '/vendor/autoload.php')) {
    require __DIR__ . '/vendor/autoload.php';

    return;
}

spl_autoload_register(static function (string $class): void {
    $roots = [
        'Qoqi\\Flow\\' => __DIR__ . '/../../../sdk/php/src/',
        'App\\' => __DIR__ . '/src/',
    ];
    foreach ($roots as $prefix => $dir) {
        if (str_starts_with($class, $prefix)) {
            $file = $dir . str_replace('\\', '/', substr($class, strlen($prefix))) . '.php';
            if (is_file($file)) {
                require $file;
            }
        }
    }
});
