#!/bin/sh

set -u

RESULT_FILE="$(mktemp /tmp/stress-ng-result.XXXXXX.yaml)"

cleanup() {
    rm -f "$RESULT_FILE"
}

trap cleanup EXIT INT TERM

stress-ng "$@" --yaml "$RESULT_FILE" >&2
status=$?

if [ -s "$RESULT_FILE" ]; then
    cat "$RESULT_FILE"
fi

exit "$status"