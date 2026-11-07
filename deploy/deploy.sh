#!/usr/bin/env bash
# Install or update pisens on the Pi from a freshly built binary, restart the
# service and roll back if the new binary does not come up healthy.
#
#   deploy/deploy.sh [--commit SHA] BINARY
#
# The deploy workflow (.github/workflows/deploy.yml) runs this from its
# checkout on the Pi's self-hosted runner. Started as a normal user it runs
# itself again through "sudo -n", so the runner user needs passwordless sudo;
# without it the script stops before it changes anything.
#
# It uses the functions of install.sh next to it and is idempotent, so the
# first run installs pisens and every later run updates it:
#
#   1. copies BINARY next to /usr/local/bin/pisens and checks that it runs
#   2. creates the pisens user (in the video group), /etc/pisens and
#      /var/lib/pisens if they are missing
#   3. creates /etc/pisens/pisens.env from the example only if it does not
#      exist, so the owner's settings are never overwritten
#   4. installs pisens.service from the repository, runs daemon-reload only
#      when it changed, and enables the service
#   5. keeps the current binary as /usr/local/bin/pisens.prev, renames the new
#      one into place and restarts the service
#   6. polls /api/health for up to 30 seconds until it reports the new version
#   7. on failure puts pisens.prev (and the previous unit) back, restarts and
#      exits non-zero
#
# BINARY is read with the caller's permissions and never run as root: the
# version check runs it as the pisens user (or nobody before that exists).
set -euo pipefail

readonly health_tries=30

self=$(realpath -- "${BASH_SOURCE[0]}")
readonly self

# The tests run the script as a normal user against a fake file system under
# PISENS_DEPLOY_ROOT, with stub commands on PATH. As root that variable is
# ignored, so it can never point a real deploy somewhere else. install.sh
# puts root in front of every path it defines.
if [[ $EUID -eq 0 ]]; then
	root=""
else
	root=${PISENS_DEPLOY_ROOT:-}
fi

# shellcheck source-path=SCRIPTDIR source=install.sh
source "$(dirname "$self")/install.sh"

# These replace the ones from install.sh.
log() { printf '%s\n' "$*"; }
die() {
	printf 'deploy.sh: %s\n' "$*" >&2
	exit 1
}

# Temporary files, removed on exit.
tmp_files=()
cleanup() {
	if [[ ${#tmp_files[@]} -gt 0 ]]; then
		rm -f -- "${tmp_files[@]}"
	fi
}
trap cleanup EXIT

usage() {
	cat <<EOF
Usage: $0 [--commit SHA] BINARY

Installs or updates pisens with BINARY as $bin_dst, restarts the $name
service and checks /api/health. If the service does not report the new
version within $health_tries seconds, the previous binary ($prev_dst) is put
back. Run as a normal user it runs itself again with "sudo -n".

Options:
  --commit SHA  the commit BINARY was built from, printed with the version
  -h, --help    show this help
EOF
}

# elevate runs the script again as root through sudo, or stops with a clear
# message before anything is changed when sudo would ask for a password.
elevate() {
	if sudo -n true 2>/dev/null; then
		exec sudo -n -- "$self" "$@"
	fi
	local user
	user=$(id -un)
	if [[ ${GITHUB_ACTIONS:-} == true ]]; then
		printf '::error title=pisens deploy::The runner user %s needs passwordless sudo to install pisens. Nothing was changed; see the log for how to set it up.\n' "$user"
	fi
	cat >&2 <<EOF
deploy.sh: the runner user $user needs passwordless sudo to install and
update pisens, and "sudo -n true" failed. Nothing was changed.

Run the runner as the Pi's first user (Raspberry Pi OS and Ubuntu give that
user passwordless sudo), or give $user passwordless sudo with:

  echo '$user ALL=(ALL) NOPASSWD: ALL' | sudo install -m 0440 /dev/stdin /etc/sudoers.d/010_$user-nopasswd
  sudo visudo -c

Then re-run this workflow. See "Automatic deployment" in the README.
EOF
	exit 1
}

# as_caller runs a command as the user who called sudo, so BINARY is read with
# that user's permissions rather than root's.
as_caller() {
	if [[ $EUID -eq 0 && -n ${SUDO_UID:-} && ${SUDO_UID} != 0 ]]; then
		setpriv --reuid="$SUDO_UID" --regid="${SUDO_GID:-$SUDO_UID}" --clear-groups -- "$@"
	else
		"$@"
	fi
}

# as_service runs a command as the pisens user, or as nobody on the first
# install, before that user exists.
as_service() {
	if [[ $EUID -ne 0 ]]; then
		"$@"
	elif getent passwd "$name" >/dev/null; then
		setpriv --reuid="$name" --regid="$name" --clear-groups --no-new-privs -- "$@"
	else
		setpriv --reuid=65534 --regid=65534 --clear-groups --no-new-privs -- "$@"
	fi
}

# version_of prints the version a pisens binary reports, without the name.
version_of() {
	local version
	version=$(as_service timeout 10 "$1" --version 2>/dev/null) || return 1
	printf '%s\n' "${version#"$name "}"
}

# health_url builds the health check URL from PISENS_LISTEN in the settings
# file, the same way install.sh reads the port.
health_url() {
	local listen="" host port
	if [[ -f $env_dst ]]; then
		listen=$(sed -n 's/^PISENS_LISTEN=//p' "$env_dst" | tail -n 1 | tr -d "\"'")
	fi
	[[ -n $listen ]] || listen=:8080
	port=${listen##*:}
	host=${listen%:*}
	case $host in
	"" | 0.0.0.0 | "[::]" | "::") host=127.0.0.1 ;;
	esac
	printf 'http://%s:%s/api/health\n' "$host" "${port:-8080}"
}

# install_copy copies src next to dst and renames it over dst, so the change
# is atomic and a running binary is never written over.
install_copy() {
	local src=$1 dst=$2 tmp
	tmp=$(mktemp "$dst.XXXXXX")
	tmp_files+=("$tmp")
	cp -- "$src" "$tmp"
	chmod 0755 "$tmp"
	mv -f -- "$tmp" "$dst"
}

# wait_healthy polls the health endpoint until it answers with status ok and,
# if given, the expected version. It prints the version it saw.
wait_healthy() {
	local want=${1:-} url body version i
	url=$(health_url)
	for ((i = 1; i <= health_tries; i++)); do
		if body=$(curl -fsS --max-time 2 "$url" 2>/dev/null) &&
			[[ $body == *'"status":"ok"'* ]]; then
			version=$(sed -n 's/.*"version":"\([^"]*\)".*/\1/p' <<<"$body")
			if [[ -z $want || $version == "$want" ]]; then
				printf '%s\n' "$version"
				return 0
			fi
		fi
		sleep 1
	done
	return 1
}

restart() {
	log "Restarting $name"
	systemctl restart "$name"
}

# What rollback puts back: the previous unit if this deploy changed it, and
# pisens.prev if this deploy replaced the binary.
unit_backup=""
swapped=false

# rollback undoes a failed deploy and exits non-zero.
rollback() {
	journalctl -u "$name" -n 30 --no-pager >&2 || true
	if [[ -z $unit_backup ]] && ! $swapped; then
		die "$name did not report healthy within $health_tries seconds and there is nothing to roll back to; check 'journalctl -u $name' and $env_dst"
	fi
	if [[ -n $unit_backup ]]; then
		log "Putting the previous $unit_dst back" >&2
		install -m 0644 "$unit_backup" "$unit_dst"
		systemctl daemon-reload
	fi
	if $swapped; then
		log "Rolling back to $prev_dst" >&2
		install_copy "$prev_dst" "$bin_dst"
	fi
	local version
	if restart && version=$(wait_healthy); then
		log "Rolled back; $name $version is running again" >&2
	else
		log "The rolled back $name did not come up healthy either; check 'journalctl -u $name'" >&2
	fi
	exit 1
}

main() {
	local commit="" src="" args=("$@")
	while [[ $# -gt 0 ]]; do
		case $1 in
		--commit)
			[[ $# -ge 2 ]] || die "--commit needs a commit SHA"
			commit=$2
			shift
			;;
		--commit=*) commit=${1#--commit=} ;;
		-h | --help)
			usage
			return 0
			;;
		-*) die "unknown option $1 (see --help)" ;;
		*)
			[[ -z $src ]] || die "only one binary can be given"
			src=$1
			;;
		esac
		shift
	done
	[[ -n $src ]] || die "no binary given (see --help)"
	if [[ -n $commit && ! $commit =~ ^[0-9a-f]{7,40}$ ]]; then
		die "invalid commit $commit"
	fi

	if [[ $EUID -ne 0 && -z $root ]]; then
		elevate "${args[@]}"
	fi

	if [[ -z $root ]]; then
		[[ -d /run/systemd/system ]] || die "systemd is not running on this machine"
	fi
	command -v curl >/dev/null || die "the health check needs curl; install it with 'apt install curl'"
	[[ -f $deploy_dir/pisens.service ]] || die "$deploy_dir/pisens.service is missing"
	[[ -f $deploy_dir/pisens.env.example ]] || die "$deploy_dir/pisens.env.example is missing"

	# Copy the binary in with the caller's permissions, so it cannot be used to
	# read a file only root may read.
	local new
	install -d -m 0755 "$(dirname "$bin_dst")"
	new=$(mktemp "$bin_dst.XXXXXX")
	tmp_files+=("$new")
	as_caller cat -- "$src" >"$new" || die "could not read $src"
	[[ -s $new ]] || die "$src is empty"
	chmod 0755 "$new"

	# Catch a broken or wrong-architecture binary before anything changes.
	local version
	version=$(version_of "$new") ||
		die "$src does not run on this machine ($(uname -m)); is it the linux/arm64 build?"

	# A missing binary means a first install, even if a unit is left over.
	local old=""
	if [[ -f $bin_dst ]]; then
		old=$(version_of "$bin_dst") || old=unknown
	fi
	log "Deploying $name $version${commit:+ (commit $commit)}"

	ensure_user
	ensure_dirs
	install_config

	if [[ -f $unit_dst ]] && ! cmp -s -- "$deploy_dir/pisens.service" "$unit_dst"; then
		unit_backup=$(mktemp)
		tmp_files+=("$unit_backup")
		cp -- "$unit_dst" "$unit_backup"
	fi
	install_unit
	if $unit_changed; then
		systemctl daemon-reload
	fi
	if ! systemctl is-enabled --quiet "$name"; then
		log "Enabling $name"
		systemctl enable "$name"
	fi

	local result
	if [[ -z $old ]]; then
		log "Installing $bin_dst"
		mv -f -- "$new" "$bin_dst"
		result="installed $version"
	elif cmp -s -- "$new" "$bin_dst"; then
		# Keep pisens.prev pointing at the binary before this one.
		log "$bin_dst is already this binary"
		result="unchanged $version, restarted"
	else
		log "Keeping the current binary as $prev_dst"
		install_copy "$bin_dst" "$prev_dst"
		log "Installing $bin_dst"
		mv -f -- "$new" "$bin_dst"
		swapped=true
		result="updated $old → $version"
	fi

	if ! restart; then
		log "Could not restart $name" >&2
		rollback
	fi
	if ! wait_healthy "$version" >/dev/null; then
		log "$name did not report version $version as healthy within $health_tries seconds" >&2
		rollback
	fi

	log ""
	log "Deployed $name $version"
	log "result:  $result"
	log "version: $version"
	log "commit:  ${commit:-unknown}"
}

main "$@"
