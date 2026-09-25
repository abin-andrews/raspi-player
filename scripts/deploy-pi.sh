#!/usr/bin/env bash
# Deploys pi-streamer to a Raspberry Pi over SSH: copies the cross-compiled
# binary + built frontend, installs a systemd unit from
# deploy/pi-streamer.service.tmpl, and enables + (re)starts it. Invoked by
# `make deploy-pi` / `make deploy-pi64`, which build the right binary and
# frontend first — usage: scripts/deploy-pi.sh <arm|arm64>
#
# Does NOT touch mpd itself: installing/configuring mpd's audio_output is a
# hardware-specific, one-time manual step (see CLAUDE.md) this script
# shouldn't guess at. It also doesn't copy any local config.json — the Pi's
# OLED port name is specific to what's plugged into the Pi, so let the
# daemon create its own config.json there via the Settings tab.
set -euo pipefail
cd "$(dirname "$0")/.."

arch="${1:?usage: deploy-pi.sh <arm|arm64>}"
pi_host="${PI_HOST:-pi@raspberrypi.local}"
pi_path="${PI_PATH:-~/pi-streamer}"

local_bin="bin/pi-streamer-${arch}"
if [ ! -f "$local_bin" ]; then
  echo "deploy-pi.sh: $local_bin not found — run 'make build-pi' or 'make build-pi64' first." >&2
  exit 1
fi
if [ ! -d web/dist ]; then
  echo "deploy-pi.sh: web/dist not found — run 'make web-build' first." >&2
  exit 1
fi

# Every ssh/scp below is multiplexed over one authenticated connection, so
# without key-based auth set up, a password is only asked for once instead
# of once per command.
ctrl_path="$(mktemp -u /tmp/pi-streamer-deploy-XXXXXX.sock)"
cleanup() { ssh -o ControlPath="$ctrl_path" -O exit "$pi_host" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> Connecting to $pi_host (you may be asked for a password once)"
ssh -o ControlMaster=yes -o ControlPath="$ctrl_path" -o ControlPersist=10m -fN "$pi_host"

ssh_c() { ssh -o ControlPath="$ctrl_path" "$pi_host" "$@"; }
scp_c() { scp -o ControlPath="$ctrl_path" "$@"; }
# Only the sudo-invoking command needs a real TTY (to prompt for its own
# password); forcing one on every call would risk a stray \r creeping into
# output we capture into a variable, like $HOME below.
ssh_sudo() { ssh -t -o ControlPath="$ctrl_path" "$pi_host" "$@"; }

echo "==> Resolving remote path on $pi_host"
remote_home=$(ssh_c 'echo $HOME')
remote_path="$pi_path"
case "$remote_path" in
  "~/"*) remote_path="$remote_home/${remote_path#\~/}" ;;
  "~") remote_path="$remote_home" ;;
esac

if [[ "$pi_host" == *@* ]]; then
  remote_user="${pi_host%%@*}"
else
  remote_user="pi"
fi

echo "==> Copying binary + frontend to $pi_host:$remote_path"
ssh_c "mkdir -p '$remote_path/web'"
scp_c "$local_bin" "$pi_host:$remote_path/pi-streamer"
ssh_c "chmod +x '$remote_path/pi-streamer'"
scp_c -r web/dist "$pi_host:$remote_path/web/"

echo "==> Installing systemd service (requires sudo on $pi_host)"
rendered="$(mktemp)"
sed \
  -e "s#__EXEC__#$remote_path/pi-streamer#g" \
  -e "s#__WORKDIR__#$remote_path#g" \
  -e "s#__USER__#$remote_user#g" \
  deploy/pi-streamer.service.tmpl > "$rendered"
scp_c "$rendered" "$pi_host:/tmp/pi-streamer.service"
rm -f "$rendered"
ssh_sudo "sudo mv /tmp/pi-streamer.service /etc/systemd/system/pi-streamer.service && \
  sudo systemctl daemon-reload && \
  sudo systemctl enable --now pi-streamer"

echo "==> Done. Check status with: ssh $pi_host sudo systemctl status pi-streamer"
echo "==> Logs with: ssh $pi_host sudo journalctl -u pi-streamer -f"
