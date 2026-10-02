#!/bin/sh
cat > /dev/null
cat <<'JSON'
{"type":"result","subtype":"success","is_error":true,"result":"Not logged in . Please run /login","total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0}}
JSON
exit 1
