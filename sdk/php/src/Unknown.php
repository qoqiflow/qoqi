<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * Thrown by a step that acted, and cannot say how: a Settled step is
 * Unknown in the flow; a plain one stops its instance, for a person.
 */
final class Unknown extends \RuntimeException
{
}
