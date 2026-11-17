#!/bin/sh
# Runs $APP_DIR/run.sh, again after a few seconds when it stops (no pod restart backoff while you work).
# Until there is a run.sh, a placeholder page says what to do; it checks for run.sh every 10 s.
app=${APP_DIR:-/srv/hackathon/app}
mkdir -p "$HOME" 2>/dev/null
while :; do
  if [ -x "$app/run.sh" ]; then
    cd "$app" && ./run.sh
    echo "entry: run.sh stopped ($?), starting it again in 3 s" >&2
    sleep 3
  else
    timeout 10 python3 /opt/hackathon/placeholder.py
  fi
done
