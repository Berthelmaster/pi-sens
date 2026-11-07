#!/usr/bin/env bash
# Replace the pisens binary on a Pi that deploy/install.sh has set up, restart
# the service and roll back if the new binary does not come up healthy.
#
#   sudo pisens-deploy [--commit SHA] BINARY
#
# The deploy workflow (.github/workflows/deploy.yml) runs this on the Pi's
# self-hosted runner. It is installed as /usr/local/sbin/pisens-deploy (owned
# by root) and the runner user may run it, and only it, as root through
# /etc/sudoers.d/pisens-deploy. "install.sh --with-deploy-hook" sets both up.
#
# Because a less trusted user hands it BINARY, the script never runs that file
# as root: it reads it with the caller's permissions and runs it only as the
# pisens user. The service, unit and settings stay as install.sh made them;
# this only swaps the binary:
#
#   1. copies BINARY next to /usr/local/bin/pisens and checks that it runs
#   2. keeps the current binary as /usr/local/bin/pisens.prev
#   3. renames the new one into place and restarts the service
#   4. polls /api/health for up to 30 seconds until it reports the new version
#   5. on failure puts pisens.prev back, restarts and exits non-zero
set -euo pipefail

readonly name=pisens
readonly health_tries=30

log() { printf '%s\n' "$*"; }
die() {
	printf 'pisens-deploy: %s\n' "$*" >&2
	exit 1
}

# The tests run the script as a normal user against a fake file system under
# PISENS_DEPLOY_ROOT, with stub commands on PATH. As root that variable is
# ignored, so it can never point a real deploy somewhere else.
if [[ $EUID -eq 0 ]]; then
	root=""
else
	root=${PISENS_DEPLOY_ROOT:-}
	[[ -n $root ]] || die "run as root, e.g. sudo $0"
fi
readonly root
readonly bin_dst=$root/usr/local/bin/pisens
readonly prev_dst=$root/usr/local/bin/pisens.prev
readonly unit_dst=$root/etc/systemd/system/pisens.service
readonly env_dst=$root/etc/pisens/pisens.env

# Temporary files next to the binary, removed on exit.
tmp_files=()
cleanup() {
	if [[ ${#tmp_files[@]} -gt 0 ]]; then
		rm -f -- "${tmp_files[@]}"
	fi
}
trap cleanup EXIT

usage() {
	cat <<EOF
Usage: sudo $0 [--commit SHA] BINARY

Installs BINARY as $bin_dst, restarts the $name service and checks
/api/health. If the service does not report the new version within
$health_tries seconds, the previous binary ($prev_dst) is put back.

Options:
  --commit SHA  the commit BINARY was built from, printed with the version
  -h, --help    show this help
EOF
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

# as_service runs a command as the pisens user.
as_service() {
	if [[ $EUID -eq 0 ]]; then
		setpriv --reuid="$name" --regid="$name" --clear-groups --no-new-privs -- "$@"
	else
		"$@"
	fi
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

# rollback puts the previous binary back after a failed deploy.
rollback() {
	journalctl -u "$name" -n 30 --no-pager >&2 || true
	if [[ ! -f $prev_dst ]]; then
		die "deploy failed and there is no $prev_dst to roll back to"
	fi
	log "Rolling back to $prev_dst" >&2
	install_copy "$prev_dst" "$bin_dst"
	local version
	if restart && version=$(wait_healthy); then
		log "Rolled back; $name $version is running again" >&2
	else
		log "The rolled back binary did not come up healthy either; check 'journalctl -u $name'" >&2
	fi
	exit 1
}

main() {
	local commit="" src=""
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

	[[ -f $unit_dst && -f $bin_dst ]] ||
		die "$name is not installed; run deploy/install.sh once first"
	command -v curl >/dev/null || die "the health check needs curl; install it with 'apt install curl'"

	# Copy the binary in with the caller's permissions, so it cannot be used to
	# read a file only root may read.
	local new
	new=$(mktemp "$bin_dst.XXXXXX")
	tmp_files+=("$new")
	as_caller cat -- "$src" >"$new" || die "could not read $src"
	[[ -s $new ]] || die "$src is empty"
	chmod 0755 "$new"

	# Catch a broken or wrong-architecture binary before it replaces a working one.
	local version
	version=$(as_service timeout 10 "$new" --version 2>/dev/null) ||
		die "$src does not run on this machine ($(uname -m)); is it the linux/arm64 build?"
	version=${version#"$name "}
	log "Deploying $name $version${commit:+ (commit $commit)}"

	if cmp -s -- "$new" "$bin_dst"; then
		# Keep pisens.prev pointing at the binary before this one.
		log "$bin_dst is already this binary"
		restart || die "could not restart $name"
		wait_healthy "$version" >/dev/null || {
			journalctl -u "$name" -n 30 --no-pager >&2 || true
			die "$name did not report healthy within $health_tries seconds"
		}
	else
		log "Keeping the current binary as $prev_dst"
		install_copy "$bin_dst" "$prev_dst"
		log "Installing $bin_dst"
		mv -f -- "$new" "$bin_dst"

		if ! restart; then
			log "Could not restart $name" >&2
			rollback
		fi
		if ! wait_healthy "$version" >/dev/null; then
			log "$name did not report version $version as healthy within $health_tries seconds" >&2
			rollback
		fi
	fi

	log ""
	log "Deployed $name $version"
	log "version: $version"
	log "commit:  ${commit:-unknown}"
}

main "$@"
