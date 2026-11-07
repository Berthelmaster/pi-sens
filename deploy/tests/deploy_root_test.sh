#!/usr/bin/env bash
# Tests for what deploy/deploy.sh does only as root: reading the binary with
# the caller's permissions and running it as the pisens user. It works on the
# real paths (/usr/local/bin/pisens, /etc/systemd/system/pisens.service) and
# creates a pisens user, so it is meant for a throwaway CI machine:
#
#   sudo env PISENS_DEPLOY_ROOT_TEST=1 deploy/tests/deploy_root_test.sh
#
# systemctl, journalctl, curl and sleep are still the stubs from
# deploy/tests/stubs.
set -euo pipefail

# shellcheck source-path=SCRIPTDIR source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

[[ $EUID -eq 0 ]] || {
	echo "run as root: sudo env PISENS_DEPLOY_ROOT_TEST=1 $0" >&2
	exit 1
}
[[ ${PISENS_DEPLOY_ROOT_TEST:-} == 1 ]] || {
	echo "this test changes /usr/local/bin and /etc; set PISENS_DEPLOY_ROOT_TEST=1 on a throwaway machine" >&2
	exit 1
}
[[ -n ${SUDO_UID:-} && $SUDO_UID != 0 ]] || {
	echo "run it through sudo from a normal user, which stands in for the runner user" >&2
	exit 1
}
for path in /usr/local/bin/pisens /etc/systemd/system/pisens.service; do
	[[ ! -e $path ]] || {
		echo "$path exists; this test only runs where pisens is not installed" >&2
		exit 1
	}
done

readonly bin=/usr/local/bin/pisens
caller=$(id -un "$SUDO_UID")
readonly caller

work=$(mktemp -d)
chmod 0755 "$work"
cleanup() {
	rm -rf "$work"
	rm -f "$bin" "$bin.prev" /etc/systemd/system/pisens.service
}
trap cleanup EXIT

if ! id -u pisens >/dev/null 2>&1; then
	useradd --system --user-group --no-create-home --shell /usr/sbin/nologin pisens
fi

# setup installs pisens VERSION on the real paths. The fake binary must be
# able to note who ran it, so the state directory is writable for everyone.
setup() {
	local installed=$1
	case_dir=$(mktemp -d "$work/case.XXXXXX")
	chmod 0755 "$case_dir"
	export STUB_BIN=$bin
	STUB_STATE=$case_dir/state
	use_stubs "$installed"
	chmod 0777 "$STUB_STATE"
	make_binary "$bin" "$installed"
	rm -f "$bin.prev"
	touch /etc/systemd/system/pisens.service
	: >"$STUB_STATE/version-users"
	chmod 0666 "$STUB_STATE/version-users"
}

caller_cannot_read() {
	! setpriv --reuid="$SUDO_UID" --regid="${SUDO_GID:-$SUDO_UID}" --clear-groups cat -- "$1" >/dev/null 2>&1
}

test_deploys_as_root() {
	setup v1.0.0
	make_binary "$case_dir/new" v1.1.0
	chown "$SUDO_UID" "$case_dir/new"
	chmod 0700 "$case_dir/new"
	run_deploy --commit abcdef1 "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "installed binary is v1.1.0" equal "$("$bin" --version)" "pisens v1.1.0"
	check "installed binary is owned by root" equal "$(stat -c '%U %a' "$bin")" "root 755"
	check "previous binary kept" equal "$("$bin.prev" --version)" "pisens v1.0.0"
	check "ran the new binary as pisens, not root" equal "$(head -n 1 "$STUB_STATE/version-users")" pisens
}

test_cannot_read_root_only_file() {
	setup v1.0.0
	make_binary "$case_dir/secret" v1.1.0
	chmod 0600 "$case_dir/secret"
	run_deploy "$case_dir/secret"
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "says it could not read the file" contains "$out" "could not read"
	check "v1.0.0 still installed" equal "$("$bin" --version)" "pisens v1.0.0"
	check "not restarted" equal "$(restarts)" 0
	check "never ran it" equal "$(wc -l <"$STUB_STATE/version-users")" 0
	check "caller $caller really cannot read it" caller_cannot_read "$case_dir/secret"
}

test_ignores_test_root_as_root() {
	setup v1.0.0
	make_binary "$case_dir/new" v1.1.0
	chown "$SUDO_UID" "$case_dir/new"
	mkdir -p "$case_dir/fake/usr/local/bin"
	status=0
	out=$(PISENS_DEPLOY_ROOT=$case_dir/fake "$deploy" "$case_dir/new" 2>&1) || status=$?
	check "exit status $status, want 0" equal "$status" 0
	check "deployed to the real path" equal "$("$bin" --version)" "pisens v1.1.0"
	check "nothing written under PISENS_DEPLOY_ROOT" test ! -e "$case_dir/fake/usr/local/bin/pisens"
}

run_tests
