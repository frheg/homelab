# pose: MediaPipe pose, hand and gesture models for camview's page (camview -web -analyze). CPU only.
#   docker build -t homelab-pose services/pose
# camview runs it with no network: docker run -i --rm --network none --read-only ... homelab-pose
# Python packages are pinned with hashes (deps.txt, mediapipe.txt: uv pip compile --generate-hashes); mediapipe is
# installed without its dependencies, because it asks for desktop OpenCV, sound and plotting it does not use here
# (matplotlib stays: mediapipe imports it). The models are checked against models.txt.
FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df AS build
WORKDIR /src
COPY deps.txt mediapipe.txt ./
RUN pip install --no-cache-dir --require-hashes --target /deps -r deps.txt \
 && pip install --no-cache-dir --require-hashes --no-deps --target /deps -r mediapipe.txt
COPY models.txt fetch_models.py ./
RUN python fetch_models.py models.txt /models

FROM python:3.12-slim-bookworm@sha256:935c3c5b12357a7e10caa9c1e6ca5692344d97cf229832ef584be2cec9fc03df
# mediapipe loads the OpenGL dispatch libraries even on the CPU
RUN apt-get update && apt-get install -y --no-install-recommends libegl1=1.6.0-1 libgles2=1.6.0-1 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /deps /deps
COPY --from=build /models /models
COPY analyze.py /app/analyze.py
ENV PYTHONPATH=/deps PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 MODELS_DIR=/models MPLCONFIGDIR=/tmp GLOG_minloglevel=2
USER 65532:65532
ENTRYPOINT ["python", "/app/analyze.py"]
