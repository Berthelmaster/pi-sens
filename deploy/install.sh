#!/usr/bin/env bash
# Install, upgrade or remove pisens as a systemd service.
#
#   sudo deploy/install.sh [BINARY]          install or upgrade
#   sudo deploy/install.sh --version vX.Y.Z  install or upgrade from a release
#   sudo deploy/install.sh --uninstall       remove the service and binary
#   sudo deploy/install.sh --uninstall --purge
#                                            also remove config, data and user
#
# BINARY is the pisens binary to install. Without it the script looks for
# pisens next to itself, then bin/pisens-linux-arm64 and bin/pisens in the
# repository it lives in, then pisens one directory up (the layout of a
# release archive).
#
# With --version it downloads that GitHub release instead, checks it against
# the release's SHA256SUMS and installs the binary, unit and settings example
# from the archive. That also works when the script is piped into bash:
#
#   curl -fsSL https://raw.githubusercontent.com/Berthelmaster/pi-sens/main/deploy/install.sh |
#     sudo bash -s -- --version latest
#
# Running it again is safe. An upgrade replaces the binary and the unit,
# keeps /etc/pisens/pisens.env and /var/lib/pisens, and restarts the service.
#
# deploy/deploy.sh, which the automatic deployment runs, sources this file for
# its functions; main only runs when the script is executed.
set -euo pipefail

# Prefix for every path below. Only deploy.sh sets it, for its tests, which
# work on a fake root directory. Run directly the script ignores it.
if (return 0 2>/dev/null); then
	root=${root:-}
else
	root=""
fi
readonly root

readonly name=pisens
readonly repo=Berthelmaster/pi-sens
readonly bin_dst=$root/usr/local/bin/pisens
readonly prev_dst=$root/usr/local/bin/pisens.prev
readonly unit_dst=$root/etc/systemd/system/pisens.service
readonly dropin_dir=$root/etc/systemd/system/pisens.service.d
readonly conf_dir=$root/etc/pisens
readonly env_dst=$root/etc/pisens/pisens.env
readonly data_dir=$root/var/lib/pisens
# The deploy hook of the first automatic deployment setup, which needed a
# sudoers rule. Deploys no longer use it; --uninstall still removes it.
readonly old_hook_dst=$root/usr/local/sbin/pisens-deploy
readonly old_sudoers_dst=$root/etc/sudoers.d/pisens-deploy

# BASH_SOURCE is empty when the script is piped into bash.
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)
readonly script_dir
# Where pisens.service and pisens.env.example come from: next to the script,
# or the downloaded archive with --version.
deploy_dir=$script_dir
# Temporary directory for a downloaded release, removed on exit, and the
# binary in it.
work_dir=""
src_binary=""

usage() {
	cat <<EOF
Usage: sudo $0 [BINARY]
       sudo $0 --version VERSION
       sudo $0 --uninstall [--purge]

Installs BINARY as $bin_dst and runs it as the $name systemd service under
its own system user. Without BINARY it looks for, in order:
  $script_dir/pisens
  $(dirname "$script_dir")/bin/pisens-linux-arm64
  $(dirname "$script_dir")/bin/pisens
  $(dirname "$script_dir")/pisens  (release archive)

Options:
  --version VERSION
               download release VERSION (e.g. v1.2.0, or latest for the newest
               non-pre-release) from github.com/$repo and install
               it instead of a local binary; needs curl
  --uninstall  stop and disable the service and remove the unit and the
               binary; keeps $conf_dir, $data_dir and the $name user
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
		"$(dirname "$script_dir")/bin/pisens" \
		"$(dirname "$script_dir")/pisens"; do
		if [[ -f $candidate ]]; then
			printf '%s\n' "$candidate"
			return 0
		fi
	done
	return 1
}

release_arch() {
	case $(uname -m) in
	aarch64 | arm64) printf 'arm64\n' ;;
	x86_64 | amd64) printf 'amd64\n' ;;
	*) die "there is no release build for $(uname -m); build pisens from source" ;;
	esac
}

# latest_tag prints the tag of the newest release that is not a pre-release,
# from where github.com/OWNER/REPO/releases/latest redirects to. Unlike the
# API this has no rate limit.
latest_tag() {
	local url
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
		die "could not look up the latest release of $repo"
	if [[ $url != */releases/tag/* ]]; then
		die "$repo has no published release yet; pass a version such as v0.1.0"
	fi
	printf '%s\n' "${url##*/releases/tag/}"
}

# download_release fetches and verifies the release archive for tag (or
# latest) and points src_binary and deploy_dir into it.
download_release() {
	local tag=$1 arch version archive base sum
	command -v curl >/dev/null || die "--version needs curl; install it with 'apt install curl'"
	arch=$(release_arch)
	if [[ $tag == latest ]]; then
		tag=$(latest_tag)
	fi
	[[ $tag == v* ]] || tag=v$tag
	[[ $tag =~ ^v[0-9A-Za-z.+-]+$ ]] || die "invalid version $tag"
	version=${tag#v}
	archive=pisens_${version}_linux_${arch}
	base=https://github.com/$repo/releases/download/$tag

	work_dir=$(mktemp -d)
	trap 'rm -rf "$work_dir"' EXIT

	log "Downloading $archive.tar.gz from release $tag"
	curl -fsSL -o "$work_dir/$archive.tar.gz" "$base/$archive.tar.gz" ||
		die "could not download $base/$archive.tar.gz; is $tag a release of $repo?"
	curl -fsSL -o "$work_dir/SHA256SUMS" "$base/SHA256SUMS" ||
		die "could not download $base/SHA256SUMS"

	sum=$(awk -v f="$archive.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$work_dir/SHA256SUMS")
	[[ -n $sum ]] || die "SHA256SUMS of $tag has no entry for $archive.tar.gz"
	printf '%s  %s\n' "$sum" "$work_dir/$archive.tar.gz" | sha256sum --check --quiet - ||
		die "checksum mismatch for $archive.tar.gz"

	tar -xzf "$work_dir/$archive.tar.gz" -C "$work_dir"
	src_binary=$work_dir/$archive/pisens
	deploy_dir=$work_dir/$archive/deploy
}

# in_group reports whether user is a supplementary member of group.
in_group() {
	local members
	members=$(getent group "$2" | cut -d: -f4)
	[[ ,$members, == *",$1,"* ]]
}

# ensure_user creates the pisens system user in the video group, or adds an
# existing one to that group.
ensure_user() {
	# Debian and Raspberry Pi OS always have the video group; create it so the
	# unit's SupplementaryGroups=video does not stop the service elsewhere.
	if ! getent group video >/dev/null; then
		log "Creating group video"
		groupadd --system video
	fi

	if getent passwd "$name" >/dev/null; then
		if ! in_group "$name" video; then
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

# ensure_dirs creates the settings and data directories. systemd hands the
# data directory to the pisens user when the service starts (StateDirectory=).
ensure_dirs() {
	install -d -m 0755 "$conf_dir"
	install -d -m 0750 "$data_dir"
}

# install_config refreshes pisens.env.example in the settings directory and
# creates pisens.env from it the first time. An existing pisens.env is never
# touched.
install_config() {
	install -m 0644 "$deploy_dir/pisens.env.example" "$conf_dir/pisens.env.example"
	if [[ -e $env_dst ]]; then
		log "Keeping existing $env_dst"
	else
		log "Creating $env_dst"
		install -m 0600 "$deploy_dir/pisens.env.example" "$env_dst"
	fi
}

# install_unit installs pisens.service from deploy_dir and sets unit_changed
# to whether that changed the installed unit (and needs a daemon-reload).
unit_changed=false
install_unit() {
	if [[ -f $unit_dst ]] && cmp -s -- "$deploy_dir/pisens.service" "$unit_dst"; then
		log "$unit_dst is up to date"
		unit_changed=false
		return 0
	fi
	log "Installing $unit_dst"
	install -d -m 0755 "$(dirname "$unit_dst")"
	install -m 0644 "$deploy_dir/pisens.service" "$unit_dst"
	unit_changed=true
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
			die "no pisens binary found; build one with 'make build-arm64', pass its path or use --version"
	fi
	[[ -f $src ]] || die "$src: no such file"
	# An absolute path, so running it below never searches PATH.
	src=$(realpath -- "$src")
	[[ -f $deploy_dir/pisens.service ]] || die "$deploy_dir/pisens.service is missing"
	[[ -f $deploy_dir/pisens.env.example ]] || die "$deploy_dir/pisens.env.example is missing"

	# Catch a binary for the wrong architecture before it replaces a working one.
	"$src" --help >/dev/null 2>&1 ||
		die "$src does not run on this machine ($(uname -m)); is it the linux/arm64 build?"
	# Builds from before --version existed fail on it, so it is only informative.
	local new_version
	new_version=$("$src" --version 2>/dev/null) || new_version="pisens (unknown version)"

	local was_active=false
	if systemctl is-active --quiet "$name"; then
		was_active=true
	fi

	ensure_user
	ensure_dirs
	install_binary "$src"
	install_unit
	install_config

	if $unit_changed; then
		systemctl daemon-reload
	fi
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
	log "$new_version is running."
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
	rm -f "$prev_dst"
	if [[ -e $old_hook_dst || -e $old_sudoers_dst ]]; then
		log "Removing the old deploy hook ($old_hook_dst, $old_sudoers_dst)"
		rm -f "$old_hook_dst" "$old_sudoers_dst"
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
	local uninstall=false purge=false binary="" release=""
	while [[ $# -gt 0 ]]; do
		case $1 in
		--uninstall) uninstall=true ;;
		--purge) purge=true ;;
		--version)
			[[ $# -ge 2 && -n $2 ]] || die "--version needs a version, e.g. --version v0.1.0 or --version latest"
			release=$2
			shift
			;;
		--version=*)
			release=${1#--version=}
			[[ -n $release ]] || die "--version needs a version, e.g. --version v0.1.0 or --version latest"
			;;
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
	if [[ -n $release ]] && $uninstall; then
		die "--version does not work with --uninstall"
	fi
	if [[ -n $release && -n $binary ]]; then
		die "give either a binary or --version, not both"
	fi
	[[ $(id -u) -eq 0 ]] || die "run as root, e.g. sudo $0"
	[[ -d /run/systemd/system ]] || die "systemd is not running on this machine"

	if $uninstall; then
		do_uninstall "$purge"
	elif [[ -n $release ]]; then
		download_release "$release"
		do_install "$src_binary"
	else
		do_install "$binary"
	fi
}

# Only when executed, not when deploy.sh sources this file. Piped into bash
# counts as executed.
if ! (return 0 2>/dev/null); then
	main "$@"
fi
