# --- terminal colors (artifacts.shell.colors: true) ---------------------------
# shellcheck disable=SC2034
if [ -t 1 ]; then
  OSB_C_BOLD=$(printf '\033[1m'); OSB_C_BLUE=$(printf '\033[1;34m')
  OSB_C_YELLOW=$(printf '\033[1;33m'); OSB_C_RED=$(printf '\033[1;31m'); OSB_C_RESET=$(printf '\033[0m')
else
  OSB_C_BOLD=''; OSB_C_BLUE=''; OSB_C_YELLOW=''; OSB_C_RED=''; OSB_C_RESET=''
fi
# --- colored output helpers ---------------------------------------------------
# log MESSAGE...: print an informational message
log() { printf '%s==>%s %s\n' "$OSB_C_BLUE" "$OSB_C_RESET" "$*"; }
# warn MESSAGE...: print a warning to stderr
warn() { printf '%sWARN:%s %s\n' "$OSB_C_YELLOW" "$OSB_C_RESET" "$*" >&2; }
# die MESSAGE...: print an error and exit
die() { printf '%sERROR:%s %s\n' "$OSB_C_RED" "$OSB_C_RESET" "$*" >&2; exit 1; }
# step N/TOTAL TITLE: announce a step
step() { printf '\n%s[%s] %s%s\n' "$OSB_C_BOLD" "$1" "$2" "$OSB_C_RESET"; }
