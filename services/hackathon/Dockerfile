# The hackathon runtime: Python 3.13 with uv, and Node 22. It runs the app in /srv/hackathon/app (mounted from kratos),
# so code changes need no new image. Build: docker build -t kratos:5000/hackathon:<tag> services/hackathon
FROM node:22-bookworm-slim@sha256:e9f565d32859f0cbfb52125eafe8be39390f24dac302dc3cdf566e8ba3ab21ee AS node

# uv 0.9.30, Python 3.13.11
FROM ghcr.io/astral-sh/uv:python3.13-bookworm-slim@sha256:531f855bda2c73cd6ef67d56b733b357cea384185b3022bd09f05e002cd144ca
RUN apt-get update \
 && apt-get install -y --no-install-recommends tini git curl \
 && rm -rf /var/lib/apt/lists/*
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
 && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
 && node --version && npm --version
COPY entry.sh placeholder.py /opt/hackathon/
USER 1000:100
ENTRYPOINT ["tini", "--", "/opt/hackathon/entry.sh"]
