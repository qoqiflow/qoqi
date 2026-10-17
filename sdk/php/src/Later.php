<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * Thrown by a step whose answer comes later, on a request of its own
 * (Daemon::complete), before the task's deadline: the daemon is told 202.
 */
final class Later extends \RuntimeException
{
}
