#!/bin/sh
# The hackathon pod runs this from /srv/hackathon/app, and again when it stops. Serve on $PORT; requests keep the /hackathon prefix ($ROOT_PATH).
# --reload restarts the server when a .py file changes.
set -e
uv sync --quiet
exec uv run uvicorn app:app --host 0.0.0.0 --port "${PORT:-8000}" --reload
