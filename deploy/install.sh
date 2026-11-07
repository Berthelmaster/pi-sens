#!/usr/bin/env bash
# Install, upgrade or remove pisens as a systemd service.
#
#   sudo deploy/install.sh [BINARY]          install or upgrade
#   sudo deploy/install.sh --uninstall       remove the service and binary
#   sudo deploy/install.sh --uninstall --purge
#                                            also remove config, data and user
#
# BINARY is the pisens binary to install. Without it the script looks for
# pisens next to itself, then bin/pisens-linux-arm64 and bin/pisens in the
# repository it lives in.
#
# Running it again is safe. An upgrade replaces the binary and the unit,
# keeps /etc/pisens/pisens.env and /var/lib/pisens, and restarts the service.
set -euo pipefail

readonly name=pisens
readonly bin_dst=/usr/local/bin/pisens
readonly unit_dst=/etc/systemd/system/pisens.service
readonly dropin_dir=/etc/systemd/system/pisens.service.d
readonly conf_dir=/etc/pisens
readonly env_dst=/etc/pisens/pisens.env
readonly data_dir=/var/lib/pisens

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir

usage() {
	cat <<EOF
Usage: sudo $0 [BINARY]
       sudo $0 --uninstall [--purge]

Installs BINARY as $bin_dst and runs it as the $name systemd service under
its own system user. Without BINARY it looks for, in order:
  $script_dir/pisens
  $(dirname "$script_dir")/bin/pisens-linux-arm64
  $(dirname "$script_dir")/bin/pisens

Options:
  --uninstall  stop and disable the service, remove the unit and the binary;
               keeps $conf_dir, $data_dir and the $name user
  --purge      with --uninstall, also remove those
  -h, --help   show this help
EOF
}

log() { printf '%s\n' "$*"; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

find_binary() {
	local candidate
	for candidate in "$script_dir/pisens" \
		"$(dirname "$script_dir")/bin/pisens-linux-arm64" \
		"$(dirname "$script_dir")/bin/pisens"; do
		if [[ -f $candidate ]]; then
			printf '%s\n' "$candidate"
			return 0
		fi
	done
	return 1
}

ensure_user() {
	# Debian and Raspberry Pi OS always have the video group; create it so the
	# unit's SupplementaryGroups=video does not stop the service elsewhere.
	if ! getent group video >/dev/null; then
		log "Creating group video"
		groupadd --system video
	fi

	if id -u "$name" >/dev/null 2>&1; then
		if [[ " $(id -nG "$name") " != *" video "* ]]; then
			log "Adding user $name to group video"
			usermod -aG video "$name"
		fi
		return 0
	fi

	local nologin
	nologin=$(command -v nologin || true)
	log "Creating system user $name"
	useradd --system --user-group --groups video \
		--home-dir "$data_dir" --no-create-home \
		--shell "${nologin:-/usr/sbin/nologin}" \
		--comment "pisens system monitor" "$name"
}

install_binary() {
	local src=$1
	if [[ $src -ef $bin_dst ]]; then
		log "$bin_dst is already the binary to install"
		return 0
	fi
	log "Installing $src as $bin_dst"
	# Copy next to the target and rename, so a running binary is replaced in
	# one step instead of being written over ("text file busy").
	local tmp
	install -d -m 0755 "$(dirname "$bin_dst")"
	tmp=$(mktemp "$bin_dst.XXXXXX")
	if ! install -m 0755 "$src" "$tmp"; then
		rm -f "$tmp"
		die "could not copy $src"
	fi
	mv -f "$tmp" "$bin_dst"
}

do_install() {
	local src=${1:-}
	if [[ -z $src ]]; then
		src=$(find_binary) ||
			die "no pisens binary found; build one with 'make build-arm64' or pass its path"
	fi
	[[ -f $src ]] || die "$src: no such file"
	# An absolute path, so running it below never searches PATH.
	src=$(realpath -- "$src")
	[[ -f $script_dir/pisens.service ]] || die "$script_dir/pisens.service is missing"
	[[ -f $script_dir/pisens.env.example ]] || die "$script_dir/pisens.env.example is missing"

	# Catch a binary for the wrong architecture before it replaces a working one.
	"$src" --help >/dev/null 2>&1 ||
		die "$src does not run on this machine ($(uname -m)); is it the linux/arm64 build?"

	local was_active=false
	if systemctl is-active --quiet "$name"; then
		was_active=true
	fi

	ensure_user
	install_binary "$src"

	log "Installing $unit_dst"
	install -m 0644 "$script_dir/pisens.service" "$unit_dst"

	install -d -m 0755 "$conf_dir"
	install -m 0644 "$script_dir/pisens.env.example" "$conf_dir/pisens.env.example"
	if [[ -e $env_dst ]]; then
		log "Keeping existing $env_dst"
	else
		log "Creating $env_dst"
		install -m 0600 "$script_dir/pisens.env.example" "$env_dst"
	fi

	systemctl daemon-reload
	systemctl enable --now "$name"
	if $was_active; then
		log "Restarting $name"
		systemctl restart "$name"
	fi

	# Give a crash on startup (bad setting, port in use) time to show.
	sleep 2
	if ! systemctl is-active --quiet "$name"; then
		journalctl -u "$name" -n 20 --no-pager >&2 || true
		die "$name did not stay running; see the log above and $env_dst"
	fi

	local listen port
	listen=$(sed -n 's/^PISENS_LISTEN=//p' "$env_dst" | tail -n 1 | tr -d "\"'")
	port=${listen##*:}
	log ""
	log "pisens is running."
	log "Dashboard: http://$(hostname):${port:-8080}/"
	log "Logs:      journalctl -u $name -f"
}

do_uninstall() {
	local purge=$1
	if [[ -f $unit_dst ]]; then
		log "Stopping and disabling $name"
		systemctl disable --now "$name" || true
		rm -f "$unit_dst"
	fi
	if $purge; then
		rm -rf "$dropin_dir"
	fi
	systemctl daemon-reload
	systemctl reset-failed "$name" 2>/dev/null || true

	if [[ -e $bin_dst ]]; then
		log "Removing $bin_dst"
		rm -f "$bin_dst"
	fi

	if ! $purge; then
		log "Kept $conf_dir, $data_dir and the $name user; run with --uninstall --purge to remove them."
		return 0
	fi

	log "Removing $conf_dir and $data_dir"
	rm -rf "$conf_dir" "$data_dir"
	if id -u "$name" >/dev/null 2>&1; then
		log "Removing user $name"
		userdel "$name"
	fi
	if getent group "$name" >/dev/null; then
		groupdel "$name"
	fi
}

main() {
	local uninstall=false purge=false binary=""
	while [[ $# -gt 0 ]]; do
		case $1 in
		--uninstall) uninstall=true ;;
		--purge) purge=true ;;
		-h | --help)
			usage
			return 0
			;;
		-*) die "unknown option $1 (see --help)" ;;
		*)
			[[ -z $binary ]] || die "only one binary can be given"
			binary=$1
			;;
		esac
		shift
	done

	if $purge && ! $uninstall; then
		die "--purge only works together with --uninstall"
	fi
	if $uninstall && [[ -n $binary ]]; then
		die "--uninstall takes no binary"
	fi
	[[ $(id -u) -eq 0 ]] || die "run as root, e.g. sudo $0"
	[[ -d /run/systemd/system ]] || die "systemd is not running on this machine"

	if $uninstall; then
		do_uninstall "$purge"
	else
		do_install "$binary"
	fi
}

main "$@"
