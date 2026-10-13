#!/usr/bin/env bash
set -euo pipefail

name=${1:-world}
for i in 1 2 3; do
	echo "hello $name ($i)"
done

if [[ -f "$HOME/.bashrc" ]]; then
	count=$(wc -l <"$HOME/.bashrc")
	printf '%s lines\n' "$count"
fi
