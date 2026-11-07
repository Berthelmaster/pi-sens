#!/usr/bin/env bash
# Tests for deploy/deploy.sh. They run it as a normal user against a fake root
# directory (PISENS_DEPLOY_ROOT) with the stub systemctl, journalctl, curl and
# sleep from deploy/tests/stubs on PATH, so they need neither root nor
# systemd. deploy_root_test.sh covers what only happens as root.
#
#   deploy/tests/deploy_test.sh
set -euo pipefail

# shellcheck source-path=SCRIPTDIR source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# setup creates a fresh fake root with pisens VERSION installed.
setup() {
	local installed=$1
	case_dir=$(mktemp -d "$work/case.XXXXXX")
	export PISENS_DEPLOY_ROOT=$case_dir/root
	export STUB_BIN=$PISENS_DEPLOY_ROOT/usr/local/bin/pisens
	STUB_STATE=$case_dir/state
	mkdir -p "$PISENS_DEPLOY_ROOT/usr/local/bin" "$PISENS_DEPLOY_ROOT/etc/systemd/system" \
		"$PISENS_DEPLOY_ROOT/etc/pisens"
	use_stubs "$installed"
	make_binary "$STUB_BIN" "$installed"
	touch "$PISENS_DEPLOY_ROOT/etc/systemd/system/pisens.service"
}

installed_version() { "$STUB_BIN" --version; }
prev_version() { "$STUB_BIN.prev" --version; }

test_deploys_new_binary() {
	setup v1.0.0
	make_binary "$case_dir/new" v1.1.0
	run_deploy --commit 0123456789abcdef0123456789abcdef01234567 "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "installed binary is v1.1.0" equal "$(installed_version)" "pisens v1.1.0"
	check "previous binary kept as pisens.prev" equal "$(prev_version)" "pisens v1.0.0"
	check "one restart" equal "$(restarts)" 1
	check "prints the version" contains "$out" "version: v1.1.0"
	check "prints the commit" contains "$out" "commit:  0123456789abcdef0123456789abcdef01234567"
	check "checks the default health URL" grep -q '^curl http://127.0.0.1:8080/api/health$' "$STUB_STATE/calls"
	check "no temporary files left" equal "$(find "$PISENS_DEPLOY_ROOT/usr/local/bin" -name 'pisens.??????' | wc -l)" 0
}

test_rolls_back_unhealthy_binary() {
	setup v1.0.0
	make_binary "$case_dir/new" v1.1.0 crash
	run_deploy --commit abcdef1 "$case_dir/new"
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "v1.0.0 is installed again" equal "$(installed_version)" "pisens v1.0.0"
	check "restarted twice (deploy and rollback)" equal "$(restarts)" 2
	check "v1.0.0 is running again" equal "$(cat "$STUB_STATE/running")" v1.0.0
	check "says it rolled back" contains "$out" "Rolled back; pisens v1.0.0 is running again"
	check "polled the health endpoint 30 times" equal "$(grep -c '^curl ' "$STUB_STATE/calls")" 31
	check "shows the service log" grep -q '^journalctl -u pisens' "$STUB_STATE/calls"
}

test_rejects_binary_that_does_not_run() {
	setup v1.0.0
	make_binary "$case_dir/new" v1.1.0 broken
	run_deploy "$case_dir/new"
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "says it does not run" contains "$out" "does not run on this machine"
	check "v1.0.0 still installed" equal "$(installed_version)" "pisens v1.0.0"
	check "no pisens.prev written" test ! -e "$PISENS_DEPLOY_ROOT/usr/local/bin/pisens.prev"
	check "not restarted" equal "$(restarts)" 0
}

test_redeploy_same_binary_keeps_prev() {
	setup v1.0.0
	make_binary "$PISENS_DEPLOY_ROOT/usr/local/bin/pisens.prev" v0.9.0
	cp "$PISENS_DEPLOY_ROOT/usr/local/bin/pisens" "$case_dir/same"
	run_deploy "$case_dir/same"
	check "exit status $status, want 0" equal "$status" 0
	check "pisens.prev still v0.9.0" equal "$(prev_version)" "pisens v0.9.0"
	check "restarted once" equal "$(restarts)" 1
	check "commit unknown" contains "$out" "commit:  unknown"
}

test_uses_listen_address_from_settings() {
	setup v1.0.0
	echo 'PISENS_LISTEN="192.168.1.20:9090"' >"$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "checks the configured address" grep -q '^curl http://192.168.1.20:9090/api/health$' "$STUB_STATE/calls"

	setup v1.0.0
	echo 'PISENS_LISTEN=[::]:9091' >"$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "checks loopback for a wildcard address" grep -q '^curl http://127.0.0.1:9091/api/health$' "$STUB_STATE/calls"
}

test_needs_install_first() {
	setup v1.0.0
	rm "$PISENS_DEPLOY_ROOT/etc/systemd/system/pisens.service"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "points to install.sh" contains "$out" "run deploy/install.sh once first"
}

test_rejects_bad_arguments() {
	setup v1.0.0
	make_binary "$case_dir/new" v1.1.0
	run_deploy --commit 'abc; rm -rf /' "$case_dir/new"
	check "invalid commit: exit status $status, want non-zero" test "$status" -ne 0
	check "invalid commit: says so" contains "$out" "invalid commit"
	run_deploy
	check "no binary: exit status $status, want non-zero" test "$status" -ne 0
	run_deploy "$case_dir/missing"
	check "missing binary: exit status $status, want non-zero" test "$status" -ne 0
	check "nothing restarted" equal "$(restarts)" 0
}

test_needs_root_without_test_root() {
	setup v1.0.0
	make_binary "$case_dir/new" v1.1.0
	status=0
	out=$(env -u PISENS_DEPLOY_ROOT "$deploy" "$case_dir/new" 2>&1) || status=$?
	if [[ $EUID -eq 0 ]]; then
		return 0 # only meaningful as a normal user
	fi
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "asks for root" contains "$out" "run as root"
}

run_tests
