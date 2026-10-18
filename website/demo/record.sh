#!/bin/sh
# The video of the console, on qoqi.dev: a daemon of its own, filled with
# instances, and the moments the video shows, played one key at a time
# beside the browser. From the root of the repository, after make build:
#
#     sh website/demo/record.sh
#
# The console is then at http://127.0.0.1:7171 (admin, admin the first
# time). Nothing of the repository is changed: the flows are deployed from
# a copy.
set -eu

port=7171
home="${TMPDIR:-/tmp}/qoqi-video"
q="$(pwd)/qoqi"
[ -x "$q" ] || { echo "make build first"; exit 1; }

export QOQI_PATH="$home/daemon" QOQI_URL="http://127.0.0.1:$port"

bold() { printf '\n\033[1m%s\033[0m\n' "$*"; }
say() { printf '\033[2m%s\033[0m\n' "$*"; }
cmd() { printf '\033[36m$\033[0m %s\n' "$*"; }
key() { printf '\n\033[2m[enter] %s\033[0m' "$*"; read -r _; }

serve() {
    "$q" serve --listen "127.0.0.1:$port" >> "$home/serve.log" 2>&1 &
    echo $! > "$home/pid"
    for _ in $(seq 50); do [ -s "$QOQI_PATH/daemon.json" ] && curl -fs "$QOQI_URL/" >/dev/null 2>&1 && return; sleep 0.1; done
}

daemon() {
    pkill -f "qoqi serve --listen 127.0.0.1:$port" 2>/dev/null || true
    sleep 0.5
}

# --- A daemon of its own, and instances to look at ---

daemon
rm -rf "$home"
mkdir -p "$home/flows"
cp examples/flows/*.qi examples/flows/*.flow.lock examples/flows/qoqi.json "$home/flows/"
serve
export QOQI_TOKEN="$(cat "$QOQI_PATH/token")"
for f in approvals shop subscriptions; do "$q" deploy "$home/flows/$f.qi" > /dev/null; done

for i in 1 2 3 4 5 6 7 8; do
    "$q" start Order "order:10$i" "{\"cart\": {\"id\": \"10$i\", \"customer\": \"c$i@example.com\", \"amount\": $((i * 1700)), \"city\": \"Lyon\"}}" > /dev/null
done
"$q" start Order order:120 '{"cart": {"id": "120", "customer": "ben@example.com", "amount": 6400, "city": "Atlantis"}}' > /dev/null
"$q" start Order order:131 '{"cart": {"id": "131", "customer": "eve@example.com", "amount": 900000, "city": "Paris"}}' > /dev/null
"$q" start Expense expense:41 '{"id": "41", "author": "sam", "amount": 54000, "patience": "2h"}' > /dev/null
"$q" start Expense expense:42 '{"id": "42", "author": "ada", "amount": 8900, "patience": "4h"}' > /dev/null
"$q" start Signup signup:joe '{"email": "joe@example.com", "plan": "Solo", "day": "1h"}' > /dev/null
sleep 6

bold "Ready: $QOQI_URL"
say "Sign in once before recording (admin, admin, then a password of your own),"
say "and leave the browser on the Overview."

# --- The video ---

key "start recording. Then, in the console: Start a flow > Expense, author lin, amount 1200, patience 1m"
say "The instance waits at its wait match: managerOf and ask are ticked, the clock counts down."

key "kill the daemon, as a crash would"
pid="$(cat "$home/pid")"
cmd "kill -9 $pid"
kill -9 "$pid"
say "the daemon is gone; the instance is a row in the store"
sleep 3
key "start it again"
cmd "qoqi serve"
serve
say "back: the instance is where it was, nothing ran twice"

key "then answer it in the console: Inbox > approved on > Send. It returns Approved(\"grace\")."

key "(optional) weeks in seconds: a trial whose day lasts three seconds"
cmd "qoqi start Signup signup:ada '{\"email\": \"ada@example.com\", \"plan\": \"Team\", \"day\": \"3s\"}'"
"$q" start Signup signup:ada '{"email": "ada@example.com", "plan": "Team", "day": "3s"}' > /dev/null
sleep 4
cmd "qoqi send signup:ada events '\"Activated\"'"
"$q" send signup:ada events '"Activated"' --key evt-1 > /dev/null
sleep 5
cmd "qoqi send signup:ada events '{\"AddedCard\": \"visa-4242\"}'"
"$q" send signup:ada events '{"AddedCard": "visa-4242"}' --key evt-3 > /dev/null
say "the trial ends at day 14, 42 seconds in, and its subscription goes on alone"

key "stop recording, and the daemon"
daemon
bold "Done. The daemon's home was $home"
