#!/bin/sh
# Gives the console of the demo a password of its own for admin, kept for
# root alone, to be changed at the first sign in: the console starts with
# admin, admin. setup.sh runs it with Caddy stopped, and starts Caddy only
# if it succeeds.
#
# The password is kept where it is read, $KEPT, once the daemon took it,
# not before: a run that stops half way leaves no password said to be
# given, and the next run gives one.
set -eu

KEPT="${KEPT:-/root/qoqi-demo-admin}"
STATE="${STATE:-/var/lib/qoqi}"
QOQI="${QOQI:-sudo -u qoqi QOQI_PATH=$STATE /usr/local/bin/qoqi}"
WAIT="${WAIT:-30}"

[ -s "$KEPT" ] && exit 0

i=0
until [ -s "$STATE/daemon.json" ]; do
    i=$((i + 1))
    if [ "$i" -gt "$WAIT" ]; then
        echo "the demo's daemon did not start: admin keeps its first password, and Caddy stays stopped" >&2
        exit 1
    fi
    sleep 1
done

umask 077
head -c 18 /dev/urandom | base64 | tr -d '/+=' > "$KEPT.new"
$QOQI users passwd admin --password-stdin < "$KEPT.new"
mv "$KEPT.new" "$KEPT"
echo "The password of admin on demo.qoqi.dev is in $KEPT"
