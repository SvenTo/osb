
# --- package management: apt -------------------------------------------------
export DEBIAN_FRONTEND=noninteractive
OSB_APT_OPTIONS=${OSB_APT_OPTIONS:-}
OSB_PKG_UPDATED=0

# pkg_update: refresh package lists (once per run)
pkg_update() {
  [ "$OSB_PKG_UPDATED" -eq 1 ] && return 0
  apt-get update -q
  OSB_PKG_UPDATED=1
}

# pkg_refresh: force refreshing package lists (e.g. after adding a repository)
pkg_refresh() { OSB_PKG_UPDATED=0; pkg_update; }

# pkg_install PACKAGE...: install packages
# shellcheck disable=SC2086
pkg_install() { pkg_update; apt-get install -y -q $OSB_APT_OPTIONS "$@"; }

# pkg_installed PACKAGE: whether a package is installed
# shellcheck disable=SC2016
pkg_installed() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q 'install ok installed'; }

# pkg_ensure PACKAGE...: install packages that are missing
pkg_ensure() {
  osb_missing=''
  for osb_p in "$@"; do pkg_installed "$osb_p" || osb_missing="$osb_missing $osb_p"; done
  # shellcheck disable=SC2086
  [ -z "$osb_missing" ] || pkg_install $osb_missing
}

# pkg_remove PACKAGE...: remove packages
pkg_remove() { apt-get remove -y -q --purge "$@"; }

# pkg_upgrade: upgrade all packages
pkg_upgrade() { pkg_update; apt-get dist-upgrade -y -q; }

# pkg_install_file FILE...: install local package files
pkg_install_file() { pkg_update; apt-get install -y -q "$@"; }

# pkg_hold PACKAGE...: prevent upgrades of packages
pkg_hold() { apt-mark hold "$@"; }

# pkg_clean: remove caches (used for container images)
pkg_clean() { apt-get clean; rm -rf /var/lib/apt/lists/*; }
