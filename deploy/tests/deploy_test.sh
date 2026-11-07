#!/usr/bin/env bash
# Tests for deploy/deploy.sh. They run it as a normal user against a fake root
# directory (PISENS_DEPLOY_ROOT) with the stub systemctl, journalctl, curl,
# sleep, getent, useradd, usermod, groupadd and sudo from deploy/tests/stubs
# on PATH, so they need neither root nor systemd. deploy_root_test.sh covers
# what only happens as root.
#
#   deploy/tests/deploy_test.sh
set -euo pipefail

# shellcheck source-path=SCRIPTDIR source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
readonly src_dir=$tests_dir/..

# fresh creates an empty fake root on which pisens was never installed, and a
# copy of deploy/ to run deploy.sh from.
fresh() {
	case_dir=$(mktemp -d "$work/case.XXXXXX")
	export PISENS_DEPLOY_ROOT=$case_dir/root
	export STUB_BIN=$PISENS_DEPLOY_ROOT/usr/local/bin/pisens
	export STUB_UNIT=$PISENS_DEPLOY_ROOT/etc/systemd/system/pisens.service
	unset STUB_SUDO_ROOT
	STUB_STATE=$case_dir/state
	mkdir -p "$PISENS_DEPLOY_ROOT" "$case_dir/deploy"
	cp "$src_dir/deploy.sh" "$src_dir/install.sh" "$src_dir/pisens.service" \
		"$src_dir/pisens.env.example" "$case_dir/deploy/"
	deploy=$case_dir/deploy/deploy.sh
	use_stubs none
	echo "video:x:44:" >"$STUB_STATE/group"
	: >"$STUB_STATE/passwd"
}

# installed creates a fake root with pisens VERSION installed and running, the
# way an earlier deploy (or install.sh) leaves it.
installed() {
	local version=$1
	fresh
	use_stubs "$version"
	mkdir -p "$PISENS_DEPLOY_ROOT/usr/local/bin" "$PISENS_DEPLOY_ROOT/etc/systemd/system" \
		"$PISENS_DEPLOY_ROOT/etc/pisens" "$PISENS_DEPLOY_ROOT/var/lib/pisens"
	make_binary "$STUB_BIN" "$version"
	cp "$src_dir/pisens.service" "$STUB_UNIT"
	cp "$src_dir/pisens.env.example" "$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"
	echo "pisens:x:901:901::/var/lib/pisens:/usr/sbin/nologin" >"$STUB_STATE/passwd"
	printf 'video:x:44:pisens\npisens:x:901:\n' >"$STUB_STATE/group"
	touch "$STUB_STATE/enabled"
}

installed_version() { "$STUB_BIN" --version; }
prev_version() { "$STUB_BIN.prev" --version; }
env_file() { cat "$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"; }
# changes_nothing reports whether a run left the system as it was: no user,
# no service calls and no files in the fake root.
changes_nothing() {
	[[ $(calls '^(useradd|usermod|groupadd|systemctl) ') -eq 0 ]] &&
		[[ -z $(find "$PISENS_DEPLOY_ROOT" -mindepth 1 -not -type d) ]]
}

test_first_install() {
	fresh
	make_binary "$case_dir/new" v1.1.0
	run_deploy --commit 0123456789abcdef0123456789abcdef01234567 "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "installed binary is v1.1.0" equal "$(installed_version)" "pisens v1.1.0"
	check "no pisens.prev" test ! -e "$STUB_BIN.prev"
	check "created the pisens user" equal "$(calls '^useradd --system --user-group --groups video .* pisens$')" 1
	check "pisens is in the video group" equal "$(getent group video)" "video:x:44:pisens"
	check "created /etc/pisens/pisens.env from the example" cmp -s "$src_dir/pisens.env.example" "$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"
	check "pisens.env is 0600" equal "$(stat -c %a "$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env")" 600
	check "copied the example too" test -f "$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env.example"
	check "created /var/lib/pisens" test -d "$PISENS_DEPLOY_ROOT/var/lib/pisens"
	check "installed the unit" cmp -s "$src_dir/pisens.service" "$STUB_UNIT"
	check "one daemon-reload" equal "$(calls '^systemctl daemon-reload$')" 1
	check "enabled the service" equal "$(calls '^systemctl enable pisens$')" 1
	check "one restart" equal "$(restarts)" 1
	check "v1.1.0 is running" equal "$(cat "$STUB_STATE/running")" v1.1.0
	check "checks the default health URL" grep -q '^curl http://127.0.0.1:8080/api/health$' "$STUB_STATE/calls"
	check "says it installed" contains "$out" "result:  installed v1.1.0"
	check "prints the version" contains "$out" "version: v1.1.0"
	check "prints the commit" contains "$out" "commit:  0123456789abcdef0123456789abcdef01234567"
	check "no temporary files left" equal "$(find "$PISENS_DEPLOY_ROOT/usr/local/bin" -name 'pisens.??????' | wc -l)" 0
}

test_update() {
	installed v1.0.0
	make_binary "$case_dir/new" v1.1.0
	run_deploy --commit abcdef1 "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "installed binary is v1.1.0" equal "$(installed_version)" "pisens v1.1.0"
	check "previous binary kept as pisens.prev" equal "$(prev_version)" "pisens v1.0.0"
	check "says what it updated" contains "$out" "result:  updated v1.0.0 → v1.1.0"
	check "one restart" equal "$(restarts)" 1
	check "no user changes" equal "$(calls '^(useradd|usermod|groupadd) ')" 0
	check "no daemon-reload for an unchanged unit" equal "$(calls '^systemctl daemon-reload')" 0
	check "not enabled again" equal "$(calls '^systemctl enable ')" 0
	check "no temporary files left" equal "$(find "$PISENS_DEPLOY_ROOT/usr/local/bin" -name 'pisens.??????' | wc -l)" 0
}

test_keeps_edited_config() {
	installed v1.0.0
	printf '# mine\nPISENS_LISTEN=:9090\n' >"$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"
	chmod 0640 "$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "pisens.env unchanged" equal "$(env_file)" "$(printf '# mine\nPISENS_LISTEN=:9090')"
	check "pisens.env mode unchanged" equal "$(stat -c %a "$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env")" 640
	check "says it kept it" contains "$out" "Keeping existing"
	check "checks the configured port" grep -q '^curl http://127.0.0.1:9090/api/health$' "$STUB_STATE/calls"

	# A settings file written before the first deploy is kept as well.
	fresh
	mkdir -p "$PISENS_DEPLOY_ROOT/etc/pisens"
	echo 'PISENS_LISTEN=:9091' >"$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "first install: exit status $status, want 0" equal "$status" 0
	check "first install: pisens.env unchanged" equal "$(env_file)" "PISENS_LISTEN=:9091"
}

test_updates_changed_unit() {
	installed v1.0.0
	echo "# an older unit" >"$STUB_UNIT"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "installed the new unit" cmp -s "$src_dir/pisens.service" "$STUB_UNIT"
	check "one daemon-reload" equal "$(calls '^systemctl daemon-reload$')" 1
}

test_adds_existing_user_to_video() {
	installed v1.0.0
	printf 'video:x:44:\npisens:x:901:\n' >"$STUB_STATE/group"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "added pisens to video" equal "$(calls '^usermod -aG video pisens$')" 1
	check "did not create the user again" equal "$(calls '^useradd ')" 0
}

test_rolls_back_unhealthy_binary() {
	installed v1.0.0
	make_binary "$case_dir/new" v1.1.0 crash
	run_deploy --commit abcdef1 "$case_dir/new"
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "v1.0.0 is installed again" equal "$(installed_version)" "pisens v1.0.0"
	check "restarted twice (deploy and rollback)" equal "$(restarts)" 2
	check "v1.0.0 is running again" equal "$(cat "$STUB_STATE/running")" v1.0.0
	check "says it rolled back" contains "$out" "Rolled back; pisens v1.0.0 is running again"
	check "polled the health endpoint 30 times" equal "$(calls '^curl ')" 31
	check "shows the service log" grep -q '^journalctl -u pisens' "$STUB_STATE/calls"
	check "no result line" test "$(grep -c '^result:' <<<"$out")" -eq 0
}

test_rolls_back_unit_too() {
	installed v1.0.0
	echo "# broken" >>"$case_dir/deploy/pisens.service"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "previous unit is back" cmp -s "$src_dir/pisens.service" "$STUB_UNIT"
	check "daemon-reload for the new unit and the rollback" equal "$(calls '^systemctl daemon-reload$')" 2
	check "v1.0.0 is installed again" equal "$(installed_version)" "pisens v1.0.0"
	check "says it rolled back" contains "$out" "Rolled back; pisens v1.0.0 is running again"
}

test_first_install_unhealthy() {
	fresh
	make_binary "$case_dir/new" v1.1.0 crash
	run_deploy "$case_dir/new"
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "says there is nothing to roll back to" contains "$out" "nothing to roll back to"
	check "restarted once" equal "$(restarts)" 1
}

test_rejects_binary_that_does_not_run() {
	installed v1.0.0
	make_binary "$case_dir/new" v1.1.0 broken
	run_deploy "$case_dir/new"
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "says it does not run" contains "$out" "does not run on this machine"
	check "v1.0.0 still installed" equal "$(installed_version)" "pisens v1.0.0"
	check "no pisens.prev written" test ! -e "$STUB_BIN.prev"
	check "not restarted" equal "$(restarts)" 0

	# Before the first install it stops before creating anything.
	fresh
	make_binary "$case_dir/new" v1.1.0 broken
	run_deploy "$case_dir/new"
	check "first install: exit status $status, want non-zero" test "$status" -ne 0
	check "first install: nothing changed" changes_nothing
}

test_redeploy_same_binary_keeps_prev() {
	installed v1.0.0
	make_binary "$STUB_BIN.prev" v0.9.0
	cp "$STUB_BIN" "$case_dir/same"
	run_deploy "$case_dir/same"
	check "exit status $status, want 0" equal "$status" 0
	check "pisens.prev still v0.9.0" equal "$(prev_version)" "pisens v0.9.0"
	check "restarted once" equal "$(restarts)" 1
	check "says it was unchanged" contains "$out" "result:  unchanged v1.0.0"
	check "commit unknown" contains "$out" "commit:  unknown"
}

test_uses_listen_address_from_settings() {
	installed v1.0.0
	echo 'PISENS_LISTEN="192.168.1.20:9090"' >"$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "checks the configured address" grep -q '^curl http://192.168.1.20:9090/api/health$' "$STUB_STATE/calls"

	installed v1.0.0
	echo 'PISENS_LISTEN=[::]:9091' >"$PISENS_DEPLOY_ROOT/etc/pisens/pisens.env"
	make_binary "$case_dir/new" v1.1.0
	run_deploy "$case_dir/new"
	check "exit status $status, want 0" equal "$status" 0
	check "checks loopback for a wildcard address" grep -q '^curl http://127.0.0.1:9091/api/health$' "$STUB_STATE/calls"
}

test_rejects_bad_arguments() {
	installed v1.0.0
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

# sudo_is_stub makes sure a test that leaves PISENS_DEPLOY_ROOT unset can
# never reach the real sudo.
sudo_is_stub() { [[ $(command -v sudo) == "$tests_dir/stubs/sudo" ]]; }

test_missing_sudo() {
	fresh
	make_binary "$case_dir/new" v1.1.0
	sudo_is_stub || {
		fail "sudo on PATH is not the stub"
		return
	}
	status=0
	out=$(env -u PISENS_DEPLOY_ROOT GITHUB_ACTIONS=true "$deploy" "$case_dir/new" 2>&1) || status=$?
	if [[ $EUID -eq 0 ]]; then
		return 0 # only meaningful as a normal user
	fi
	check "exit status $status, want non-zero" test "$status" -ne 0
	check "says passwordless sudo is needed" contains "$out" "needs passwordless sudo"
	check "says how to set it up" contains "$out" "NOPASSWD: ALL"
	check "adds an annotation on GitHub Actions" contains "$out" "::error title=pisens deploy::"
	check "only asked sudo -n" equal "$(calls '^sudo ')" "$(calls '^sudo -n true$')"
	check "nothing changed" changes_nothing
}

test_runs_itself_through_sudo() {
	fresh
	make_binary "$case_dir/new" v1.1.0
	sudo_is_stub || {
		fail "sudo on PATH is not the stub"
		return
	}
	export STUB_SUDO_ROOT=$PISENS_DEPLOY_ROOT
	status=0
	out=$(env -u PISENS_DEPLOY_ROOT "$deploy" --commit abcdef1 "$case_dir/new" 2>&1) || status=$?
	unset STUB_SUDO_ROOT
	if [[ $EUID -eq 0 ]]; then
		return 0 # only meaningful as a normal user
	fi
	check "exit status $status, want 0" equal "$status" 0
	check "ran itself with sudo -n" equal "$(calls "^sudo -n -- $deploy --commit abcdef1 $case_dir/new\$")" 1
	check "installed v1.1.0" equal "$(installed_version)" "pisens v1.1.0"
	check "says it installed" contains "$out" "result:  installed v1.1.0"
}

run_tests
