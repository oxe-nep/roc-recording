#!/usr/bin/env bash
# Manage roc-recording on the capture host (systemd + orphan cleanup).
set -euo pipefail

SERVICE="${ROC_SERVICE:-roc-recording}"
APP_ROOT="${ROC_APP_ROOT:-/opt/application/roc-recording}"
BACKEND_DIR="$APP_ROOT/backend"
BIN="$BACKEND_DIR/roc-recording"
UNIT_SRC="$APP_ROOT/deploy/roc-recording.service"
ENV_SRC="$APP_ROOT/deploy/roc-recording.env.example"
ENV_DST="/etc/roc-recording.env"

usage() {
  cat <<EOF
Usage: $(basename "$0") <command>

Commands:
  install   Install/update systemd unit + env file, enable service
  build     go build backend binary
  start     Start service
  stop      Stop service (kills Go + child FFmpeg in cgroup)
  restart   Restart service
  status    Show service status
  logs      Follow journal logs
  cleanup   Stop service and kill leftover roc-recording / related ffmpeg
  help      Show this help
EOF
}

need_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    echo "This command needs root (sudo)." >&2
    exit 1
  fi
}

cmd_build() {
  echo "Building backend..."
  (cd "$BACKEND_DIR" && go build -o roc-recording ./cmd/server)
  echo "OK: $BIN"
}

cmd_install() {
  need_root
  if [[ ! -f "$UNIT_SRC" ]]; then
    echo "Missing unit file: $UNIT_SRC" >&2
    exit 1
  fi
  if [[ ! -x "$BIN" ]]; then
    cmd_build
  fi
  install -m 0644 "$UNIT_SRC" "/etc/systemd/system/${SERVICE}.service"
  if [[ ! -f "$ENV_DST" ]]; then
    install -m 0600 "$ENV_SRC" "$ENV_DST"
    echo "Created $ENV_DST – edit API_KEY / PUBLIC_URL if needed."
  else
    echo "Keeping existing $ENV_DST"
  fi
  systemctl daemon-reload
  systemctl enable "$SERVICE"
  echo "Installed and enabled ${SERVICE}.service"
  echo "Next: $(basename "$0") start"
}

cmd_start() {
  need_root
  systemctl start "$SERVICE"
  systemctl --no-pager --full status "$SERVICE" || true
}

cmd_stop() {
  need_root
  systemctl stop "$SERVICE" || true
  # Extra safety: anything still holding DeckLink/ffmpeg from this app.
  cleanup_orphans
  echo "Stopped."
}

cmd_restart() {
  need_root
  systemctl restart "$SERVICE"
  systemctl --no-pager --full status "$SERVICE" || true
}

cmd_status() {
  systemctl --no-pager --full status "$SERVICE" || true
  echo
  echo "Related processes:"
  pgrep -af 'roc-recording|ffmpeg.*DeckLink|ffmpeg.*udp://127.0.0.1:210' || echo "(none)"
}

cmd_logs() {
  journalctl -u "$SERVICE" -f -n 100
}

cleanup_orphans() {
  # Kill backend binary if somehow outside systemd.
  pkill -TERM -x roc-recording 2>/dev/null || true
  sleep 1
  pkill -KILL -x roc-recording 2>/dev/null || true

  # Kill FFmpeg children that still bind our local UDP recording feeds / DeckLink inputs.
  # Narrow patterns to avoid killing unrelated ffmpeg on the host.
  pkill -TERM -f 'ffmpeg.*(DeckLink IP 100G|udp://127\.0\.0\.1:210[0-9]{2})' 2>/dev/null || true
  sleep 1
  pkill -KILL -f 'ffmpeg.*(DeckLink IP 100G|udp://127\.0\.0\.1:210[0-9]{2})' 2>/dev/null || true
}

cmd_cleanup() {
  need_root
  systemctl stop "$SERVICE" 2>/dev/null || true
  cleanup_orphans
  echo "Cleanup done. Remaining:"
  pgrep -af 'roc-recording|ffmpeg.*DeckLink|ffmpeg.*udp://127.0.0.1:210' || echo "(none)"
}

main() {
  local cmd="${1:-help}"
  case "$cmd" in
    install) cmd_install ;;
    build) cmd_build ;;
    start) cmd_start ;;
    stop) cmd_stop ;;
    restart) cmd_restart ;;
    status) cmd_status ;;
    logs) cmd_logs ;;
    cleanup) cmd_cleanup ;;
    help|-h|--help) usage ;;
    *)
      usage
      exit 1
      ;;
  esac
}

main "$@"
