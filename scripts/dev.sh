#!/usr/bin/env bash
# Brings up the full dev stack (mpd, search-indexer, daemon, frontend dev
# server) in one foreground command; Ctrl+C stops all three app processes
# together (mpd is left running, since it's shared system state, not
# something scoped to a dev session).
set -e
cd "$(dirname "$0")/.."

# mpd_reachable checks 127.0.0.1:6600 directly (bash's /dev/tcp) rather than
# shelling out to mpc, which may not be installed.
mpd_reachable() {
  (exec 3<>/dev/tcp/127.0.0.1/6600) 2>/dev/null
}

# ensure_mpd fails fast with a clear message if mpd can't be reached and
# can't be started, instead of letting the daemon silently log.Fatalf on it
# minutes into a dev session.
ensure_mpd() {
  if mpd_reachable; then
    return 0
  fi

  echo "dev.sh: mpd isn't reachable on 127.0.0.1:6600 — trying to start it via systemd." >&2
  if ! command -v systemctl >/dev/null || ! systemctl list-unit-files 2>/dev/null | grep -q '^mpd\.service'; then
    echo "dev.sh: no mpd systemd unit found. Start mpd yourself, then re-run 'make dev'." >&2
    return 1
  fi

  if ! sudo systemctl start mpd; then
    echo "dev.sh: 'sudo systemctl start mpd' failed. Start mpd yourself, then re-run 'make dev'." >&2
    return 1
  fi

  for _ in 1 2 3 4 5 6 7 8 9 10; do
    mpd_reachable && return 0
    sleep 0.5
  done
  echo "dev.sh: mpd service started but isn't accepting connections on 127.0.0.1:6600 yet." \
    "Check 'systemctl status mpd' / /etc/mpd.conf, then re-run 'make dev'." >&2
  return 1
}

ensure_mpd || exit 1

trap 'kill 0' EXIT INT TERM

make build-indexer
make build

if [ -f .env ]; then
  set -a
  . ./.env
  set +a
fi

./bin/search-indexer &
./bin/pi-streamer "$@" &

if [ ! -d web/node_modules ]; then
  npm --prefix web install
fi
npm --prefix web run dev &

wait
