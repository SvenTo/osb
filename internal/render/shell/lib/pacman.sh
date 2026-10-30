
# --- package management: pacman ----------------------------------------------
OSB_PKG_UPDATED=0

# pkg_update: synchronize and upgrade once per run (Arch does not support partial upgrades)
pkg_update() {
  [ "$OSB_PKG_UPDATED" -eq 1 ] && return 0
  pacman -Syu --noconfirm
  OSB_PKG_UPDATED=1
}

# pkg_refresh: force refreshing package databases (e.g. after adding a repository)
pkg_refresh() { OSB_PKG_UPDATED=0; pkg_update; }

# pkg_install PACKAGE...: install packages
pkg_install() { pkg_update; pacman -S --noconfirm --needed "$@"; }

# pkg_installed PACKAGE: whether a package is installed
pkg_installed() { pacman -Qi "$1" >/dev/null 2>&1; }

# pkg_ensure PACKAGE...: install packages that are missing
pkg_ensure() {
  osb_missing=''
  for osb_p in "$@"; do pkg_installed "$osb_p" || osb_missing="$osb_missing $osb_p"; done
  # shellcheck disable=SC2086
  [ -z "$osb_missing" ] || pkg_install $osb_missing
}

# pkg_remove PACKAGE...: remove packages
pkg_remove() { pacman -Rns --noconfirm "$@"; }

# pkg_upgrade: upgrade all packages
pkg_upgrade() { OSB_PKG_UPDATED=0; pkg_update; }

# pkg_install_file FILE...: install local package files
pkg_install_file() { pacman -U --noconfirm --needed "$@"; }

# pkg_hold PACKAGE...: prevent upgrades of packages (IgnorePkg)
pkg_hold() {
  for osb_p in "$@"; do
    if grep -Eq '^[[:space:]]*IgnorePkg[[:space:]]*=' /etc/pacman.conf; then
      grep -Eq "^[[:space:]]*IgnorePkg[[:space:]]*=.*[[:space:]=]$osb_p([[:space:]]|\$)" /etc/pacman.conf ||
        sed -i -E "s/^([[:space:]]*IgnorePkg[[:space:]]*=.*)\$/\\1 $osb_p/" /etc/pacman.conf
    else
      sed -i -E "s/^\\[options\\]\$/[options]\\nIgnorePkg = $osb_p/" /etc/pacman.conf
    fi
  done
}

# pkg_clean: remove caches (used for container images)
pkg_clean() { pacman -Scc --noconfirm >/dev/null 2>&1 || true; rm -rf /var/cache/pacman/pkg/*; }
