#!/bin/sh
set -eu

DATA_DIR="${VPSPANEL_DATA:-/data}"

# Pass through arbitrary commands (e.g. `sh`), while keeping vps-panel
# flags and subcommands running as the unprivileged vpspanel user.
if [ "$#" -gt 0 ] && [ "${1#-}" = "$1" ]; then
  case "$1" in
    reset-admin|keygen|sign)
      ;;
    *)
      exec "$@"
      ;;
  esac
fi

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR"
  # Avoid dereferencing symlinks inside DATA_DIR while fixing ownership as root.
  find "$DATA_DIR" -xdev -exec chown -h vpspanel:vpspanel {} +
  exec su-exec vpspanel /opt/vps-panel/vps-panel "$@"
fi

exec /opt/vps-panel/vps-panel "$@"
