#!/bin/sh
# A stand-in for the claude CLI. It records how it was invoked and emits the
# JSON envelope shape the agent runner parses.
capture="${CLAUDE_TEST_CAPTURE:-/dev/null}"
printf '%s\n' "$*" > "$capture.args"
cat > "$capture.stdin"
if [ -n "$CLAUDE_TEST_BADJSON" ]; then
  printf 'not json at all'
  exit 0
fi
cat <<'JSON'
{"type":"result","subtype":"success","is_error":false,"duration_ms":1234,
 "result":"chatter\n=====REPORT=====\nthe report body\n",
 "total_cost_usd":0.0123,
 "usage":{"input_tokens":111,"output_tokens":222}}
JSON
