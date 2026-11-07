# shellcheck shell=bash disable=SC2034 # status and out are for the tests that source this
# Helpers shared by the deploy.sh tests.

tests_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly tests_dir
readonly deploy=$tests_dir/../deploy.sh
failures=0
status=0
out=""
current_test=""

# make_binary writes a fake pisens to path that prints "pisens VERSION" for
# --version and records which user ran it. With "crash" the stub systemctl
# treats it as a service that exits on startup; with "broken" it does not
# run at all, like a binary for another architecture.
make_binary() {
	local path=$1 version=$2 mode=${3:-}
	if [[ $mode == broken ]]; then
		printf '\x7fELF not a binary for this machine\n' >"$path"
		chmod 0755 "$path"
		return
	fi
	cat >"$path" <<SCRIPT
#!/usr/bin/env bash
# mode: ${mode:-ok}
if [[ \${1:-} == --version ]]; then
	id -un >>"\$STUB_STATE/version-users" 2>/dev/null || true
	echo "pisens $version"
	exit 0
fi
exit 0
SCRIPT
	chmod 0755 "$path"
}

# use_stubs puts the stub commands first on PATH and starts with a clean
# record of calls, with the fake service running version.
use_stubs() {
	local running=$1
	mkdir -p "$STUB_STATE"
	echo "$running" >"$STUB_STATE/running"
	: >"$STUB_STATE/calls"
	export STUB_STATE
	export PATH=$tests_dir/stubs:$orig_path
}
orig_path=$PATH

# run_deploy runs deploy.sh with the given arguments, keeping its exit code
# in status and its output in out.
run_deploy() {
	status=0
	out=$("$deploy" "$@" 2>&1) || status=$?
}

restarts() { grep -c '^systemctl restart pisens$' "$STUB_STATE/calls" || true; }

fail() {
	printf '  FAIL: %s\n' "$*"
	# On GitHub Actions also as an annotation, which shows without the log.
	if [[ ${GITHUB_ACTIONS:-} == true ]]; then
		printf '::error title=%s::%s: %s\n' "$(basename "$0")" "$current_test" "$*"
	fi
	printf '%s\n' "$out" | sed 's/^/    | /'
	failures=$((failures + 1))
}
check() {
	local what=$1
	shift
	"$@" || fail "$what"
}
equal() { [[ $1 == "$2" ]]; }
contains() { [[ $1 == *"$2"* ]]; }

# run_tests runs every test_* function and exits non-zero if a check failed.
run_tests() {
	local t before
	for t in $(declare -F | awk '$3 ~ /^test_/ { print $3 }'); do
		current_test=$t
		before=$failures
		"$t"
		if [[ $failures -eq $before ]]; then
			printf 'ok   %s\n' "$t"
		else
			printf 'FAIL %s\n' "$t"
		fi
	done
	if [[ $failures -gt 0 ]]; then
		printf '%d check(s) failed\n' "$failures"
		exit 1
	fi
	echo "all tests passed"
}
