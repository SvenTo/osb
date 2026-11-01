# --- osb helper library ------------------------------------------------------
OSB_STEP=0
OSB_STEPS=${OSB_STEPS:-0}
OSB_FAILED=0

if [ -t 1 ]; then
  OSB_C_BOLD=$(printf '\033[1m'); OSB_C_BLUE=$(printf '\033[1;34m')
  OSB_C_YELLOW=$(printf '\033[1;33m'); OSB_C_RED=$(printf '\033[1;31m'); OSB_C_RESET=$(printf '\033[0m')
else
  OSB_C_BOLD=''; OSB_C_BLUE=''; OSB_C_YELLOW=''; OSB_C_RED=''; OSB_C_RESET=''
fi

# log MESSAGE...: print an informational message
log() { printf '%s==>%s %s\n' "$OSB_C_BLUE" "$OSB_C_RESET" "$*"; }

# warn MESSAGE...: print a warning to stderr
warn() { printf '%sWARN:%s %s\n' "$OSB_C_YELLOW" "$OSB_C_RESET" "$*" >&2; }

# die MESSAGE...: print an error and exit
die() { printf '%sERROR:%s %s\n' "$OSB_C_RED" "$OSB_C_RESET" "$*" >&2; exit 1; }

# step TITLE: announce the next step
step() {
  OSB_STEP=$((OSB_STEP + 1))
  printf '\n%s[%s/%s] %s%s\n' "$OSB_C_BOLD" "$OSB_STEP" "$OSB_STEPS" "$*" "$OSB_C_RESET"
}

# has_cmd NAME: check whether a command exists
has_cmd() { command -v "$1" >/dev/null 2>&1; }

# require_root: abort unless running as root
require_root() { [ "$(id -u)" -eq 0 ] || die "this script must run as root"; }

# check_flavor ID...: abort unless /etc/os-release matches one of the IDs
check_flavor() {
  [ -r /etc/os-release ] || die "cannot read /etc/os-release"
  # shellcheck disable=SC1091
  osb_ids=$(. /etc/os-release && echo "${ID:-} ${ID_LIKE:-}")
  for osb_want in "$@"; do
    for osb_id in $osb_ids; do
      [ "$osb_id" = "$osb_want" ] && return 0
    done
  done
  die "this script was generated for '$*' but the system is '$osb_ids'"
}

# as_user USER COMMAND...: run a command as another user with its HOME
as_user() {
  osb_user=$1; shift
  if [ "$(id -un)" = "$osb_user" ]; then "$@"; return; fi
  osb_home=$(getent passwd "$osb_user" | cut -d: -f6)
  # setpriv opens no PAM session: pam_limits can fail in containers (e.g. realtime limits)
  if has_cmd setpriv; then
    setpriv --reuid="$osb_user" --regid="$(id -g "$osb_user")" --init-groups \
      env HOME="$osb_home" USER="$osb_user" LOGNAME="$osb_user" "$@"
  elif has_cmd runuser; then
    runuser -u "$osb_user" -- env HOME="$osb_home" USER="$osb_user" LOGNAME="$osb_user" "$@"
  else
    # shellcheck disable=SC2016
    su -s /bin/sh "$osb_user" -c '"$0" "$@"' -- "$@"
  fi
}

# set_owner PATH OWNER GROUP: change ownership if OWNER or GROUP are given
set_owner() {
  if [ -n "$2" ] && [ -n "$3" ]; then chown "$2:$3" "$1"
  elif [ -n "$2" ]; then chown "$2" "$1"
  elif [ -n "$3" ]; then chgrp "$3" "$1"
  fi
}

# write_file PATH [MODE] [OWNER] [GROUP] < CONTENT: atomically write stdin to PATH
write_file() {
  osb_dir=$(dirname "$1")
  [ -d "$osb_dir" ] || mkdir -p "$osb_dir"
  osb_tmp=$(mktemp "$1.osb.XXXXXX")
  cat > "$osb_tmp"
  chmod "${2:-0644}" "$osb_tmp"
  set_owner "$osb_tmp" "${3:-}" "${4:-}"
  mv -f "$osb_tmp" "$1"
}

# write_file_b64 PATH [MODE] [OWNER] [GROUP] < BASE64: write base64 encoded stdin to PATH
write_file_b64() {
  osb_b64=$(mktemp)
  base64 -d > "$osb_b64"
  write_file "$@" < "$osb_b64"
  rm -f "$osb_b64"
}

# append_file PATH < CONTENT: append stdin to PATH unless it is already contained
append_file() {
  osb_tmp=$(mktemp)
  cat > "$osb_tmp"
  if [ -f "$1" ] && awk 'NR == FNR { if (!($0 in want)) n++; want[$0] = 1; next } ($0 in want) && !seen[$0]++ { found++ } END { exit !(found >= n) }' "$osb_tmp" "$1"; then
    rm -f "$osb_tmp"; return 0
  fi
  mkdir -p "$(dirname "$1")"
  [ ! -s "$1" ] || [ -z "$(tail -c1 "$1")" ] || echo >> "$1"
  cat "$osb_tmp" >> "$1"
  rm -f "$osb_tmp"
}

# download URL PATH [SHA256] [MODE] [OWNER] [GROUP]: download a file, optionally verify it
download() {
  if ! has_cmd curl && ! has_cmd wget; then pkg_ensure curl ca-certificates; fi
  osb_dl=$(mktemp)
  if has_cmd curl; then curl -fsSL --retry 3 -o "$osb_dl" "$1"
  else wget -q -O "$osb_dl" "$1"; fi
  if [ -n "${3:-}" ]; then
    echo "$3  $osb_dl" | sha256sum -c - >/dev/null 2>&1 || { rm -f "$osb_dl"; die "checksum mismatch for $1"; }
  fi
  write_file "$2" "${4:-0644}" "${5:-}" "${6:-}" < "$osb_dl"
  rm -f "$osb_dl"
}

# ensure_dir PATH [MODE] [OWNER] [GROUP]: create a directory
ensure_dir() {
  [ -d "$1" ] || mkdir -p "$1"
  [ -z "${2:-}" ] || chmod "$2" "$1"
  set_owner "$1" "${3:-}" "${4:-}"
}

# ensure_line FILE LINE [REGEX]: make sure LINE is in FILE; replace lines matching REGEX
ensure_line() {
  [ -f "$1" ] || { mkdir -p "$(dirname "$1")"; : > "$1"; }
  if [ -n "${3:-}" ] && grep -Eq -- "$3" "$1"; then
    osb_tmp=$(mktemp)
    OSB_LINE=$2 awk -v re="$3" '$0 ~ re { if (!done) print ENVIRON["OSB_LINE"]; done=1; next } { print }' "$1" > "$osb_tmp"
    cat "$osb_tmp" > "$1"; rm -f "$osb_tmp"
  elif ! grep -qxF -- "$2" "$1"; then
    [ ! -s "$1" ] || [ -z "$(tail -c1 "$1")" ] || echo >> "$1"
    printf '%s\n' "$2" >> "$1"
  fi
}

# remove_line FILE REGEX: delete lines matching REGEX
remove_line() {
  [ -f "$1" ] || return 0
  osb_tmp=$(mktemp)
  awk -v re="$2" '$0 !~ re' "$1" > "$osb_tmp"
  cat "$osb_tmp" > "$1"; rm -f "$osb_tmp"
}

# set_ini FILE SECTION KEY VALUE: set KEY=VALUE in [SECTION] (empty SECTION = top)
set_ini() {
  [ -f "$1" ] || { mkdir -p "$(dirname "$1")"; : > "$1"; }
  osb_tmp=$(mktemp)
  OSB_V=$4 awk -v sec="$2" -v key="$3" '
    function emit() { print key "=" ENVIRON["OSB_V"]; done = 1 }
    BEGIN { cur = ""; done = 0 }
    /^[ \t]*\[.*\][ \t]*$/ {
      if (cur == sec && !done) emit()
      s = $0; gsub(/^[ \t]*\[|\][ \t]*$/, "", s); cur = s
      print; next
    }
    cur == sec && !done {
      k = $0; sub(/[ \t]*[=:].*$/, "", k); gsub(/^[ \t]+/, "", k)
      if (k == key && $0 ~ /[=:]/) { emit(); next }
    }
    { print }
    END {
      if (!done) {
        if (cur != sec && sec != "") { print ""; print "[" sec "]" }
        print key "=" ENVIRON["OSB_V"]
      }
    }' "$1" > "$osb_tmp"
  cat "$osb_tmp" > "$1"; rm -f "$osb_tmp"
}

# set_env_var FILE KEY VALUE: set KEY=VALUE in a shell-style environment file
set_env_var() {
  ensure_line "$1" "$2=$3" "^[[:space:]]*(export[[:space:]]+)?$2="
}

# set_json FILE PATH VALUE: set VALUE (JSON) at PATH (JSON array) using jq
set_json() {
  [ -s "$1" ] || { mkdir -p "$(dirname "$1")"; echo '{}' > "$1"; }
  osb_tmp=$(mktemp)
  jq --argjson p "$2" --argjson v "$3" 'setpath($p; $v)' "$1" > "$osb_tmp"
  cat "$osb_tmp" > "$1"; rm -f "$osb_tmp"
}

# set_yaml FILE PATH VALUE: set VALUE (JSON) at PATH (JSON array) using yq
set_yaml() {
  [ -s "$1" ] || { mkdir -p "$(dirname "$1")"; echo '{}' > "$1"; }
  if yq --version 2>&1 | grep -q mikefarah; then
    OSB_P=$2 OSB_V=$3 yq -i 'setpath(env(OSB_P); env(OSB_V))' "$1"
  else
    osb_tmp=$(mktemp)
    yq -y --argjson p "$2" --argjson v "$3" 'setpath($p; $v)' "$1" > "$osb_tmp"
    cat "$osb_tmp" > "$1"; rm -f "$osb_tmp"
  fi
}

# authorize_key USER KEY: add an SSH public key to the user's authorized_keys
authorize_key() {
  osb_home=$(getent passwd "$1" | cut -d: -f6)
  [ -n "$osb_home" ] || die "user $1 does not exist"
  osb_group=$(id -gn "$1")
  ensure_dir "$osb_home/.ssh" 0700 "$1" "$osb_group"
  touch "$osb_home/.ssh/authorized_keys"
  grep -qxF -- "$2" "$osb_home/.ssh/authorized_keys" || printf '%s\n' "$2" >> "$osb_home/.ssh/authorized_keys"
  chmod 0600 "$osb_home/.ssh/authorized_keys"
  chown "$1:$osb_group" "$osb_home/.ssh/authorized_keys"
}

# wait_for TIMEOUT COMMAND...: retry COMMAND every second until it succeeds
wait_for() {
  osb_deadline=$(( $(date +%s) + $1 )); shift
  until "$@" >/dev/null 2>&1; do
    [ "$(date +%s)" -lt "$osb_deadline" ] || die "timed out waiting for: $*"
    sleep 1
  done
}

# port_open HOST PORT: whether a TCP port accepts connections
port_open() {
  if has_cmd nc; then nc -z "$1" "$2"
  else bash -c "exec 3<>/dev/tcp/$1/$2" 2>/dev/null; fi
}

# random_password [LENGTH]: print a random alphanumeric password
random_password() {
  LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c "${1:-20}"
}

# systemd_available: whether systemd is the running init system
systemd_available() { [ -d /run/systemd/system ]; }

# unit_enable UNIT...: enable systemd units (works without a running systemd)
unit_enable() {
  if has_cmd systemctl; then systemctl enable "$@"
  else warn "systemctl not found, cannot enable $*"; fi
}

# unit_start UNIT...: start (or restart) units if systemd is running
unit_restart() {
  if systemd_available; then systemctl restart "$@"
  else warn "systemd is not running, not restarting $*"; fi
}

# daemon_reload: reload systemd if it is running
daemon_reload() { if systemd_available; then systemctl daemon-reload; fi; }

# osb_finish: print a summary
osb_finish() {
  if [ "$OSB_FAILED" -gt 0 ]; then
    warn "finished with $OSB_FAILED failed step(s)"
  else
    log "finished successfully"
  fi
}
