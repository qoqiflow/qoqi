<?php

declare(strict_types=1);

namespace Qoqi\Flow;

/**
 * A request whose signature is wrong, missing, or too old.
 */
final class SignatureError extends \RuntimeException
{
}
