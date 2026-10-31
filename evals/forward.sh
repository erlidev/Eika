#!/bin/sh
# Opens the SSH forward to the bench's chat model (evals/models/README.md,
# "The chat model") when EIKA_LIVE_ENDPOINT is on loopback and its port does
# not answer. An endpoint that is unset or elsewhere is left alone: the live
# tests skip or connect on their own. The key is never read here.
set -eu

env_file="$HOME/.config/eika/live.env"
remote_host=ubuntu-5090
remote_addr=10.10.10.2:8091

endpoint="${EIKA_LIVE_ENDPOINT-}"
if [ -z "$endpoint" ] && [ -f "$env_file" ]; then
	endpoint=$(sed -n 's/^\(export \)\{0,1\}EIKA_LIVE_ENDPOINT=//p' "$env_file" | tail -n 1 | tr -d "\"'")
fi

case "$endpoint" in
http://127.0.0.1:* | http://localhost:*) ;;
*) exit 0 ;;
esac
port=${endpoint##*:}
port=${port%%/*}

if curl -s --max-time 3 -o /dev/null "http://127.0.0.1:$port/health"; then
	exit 0
fi

echo "forward: 127.0.0.1:$port does not answer, opening the SSH forward to $remote_host"
mkdir -p "$HOME/.ssh/cm"
ssh -fN -o ControlMaster=auto -o ControlPath="$HOME/.ssh/cm/%r@%h:%p" -o ControlPersist=4h \
	-o ExitOnForwardFailure=yes \
	-L "127.0.0.1:$port:$remote_addr" "$remote_host" ||
	echo "forward: ssh failed; the live tests will fail with the way to open it by hand"
