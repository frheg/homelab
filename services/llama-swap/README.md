# llama-swap

The GPU gateway image (`platform/llm`): llama-swap on a llama.cpp built here. Use it when the upstream
`ghcr.io/mostlygeek/llama-swap:cuda` image lags a llama.cpp fix a model needs; when upstream catches up, mirror theirs
again (`scripts/mirror`) and drop this.

Current: llama-swap v275 on llama.cpp b12619 (commit c622f62, has PR 32704 for Inkling 2), CUDA 12.8, sm_120 only
(the RTX 5060 Ti), built 2026-11-22.

Language: Dockerfile only; llama.cpp is built by its own `.devops/cuda.Dockerfile`.

New image:

    git clone --depth 1 --branch b<N> https://github.com/ggml-org/llama.cpp /tmp/llama.cpp
    sed -i 's/-j$(nproc)/-j8/' /tmp/llama.cpp/.devops/cuda.Dockerfile   # nvcc at -j16 can run out of RAM
    docker build -f /tmp/llama.cpp/.devops/cuda.Dockerfile --target server --build-arg CUDA_VERSION=12.8.1 \
        --build-arg CUDA_DOCKER_ARCH=120 -t llama-cpp-server-cuda:b<N> /tmp/llama.cpp
    docker build --build-arg LLAMA=llama-cpp-server-cuda:b<N> -t kratos:5000/llama-swap:v275-b<N> services/llama-swap
    skopeo --insecure-policy copy --dest-tls-verify=false docker-daemon:kratos:5000/llama-swap:v275-b<N> docker://kratos:5000/llama-swap:v275-b<N>
    skopeo inspect --tls-verify=false --format '{{.Digest}}' docker://kratos:5000/llama-swap:v275-b<N>

then pin the digest in `platform/llm/llm.yaml` and `platform/llm/llm-confidential.yaml`. Before that, check that every
flag in `platform/llm/models/*.yaml` is still in `llama-server --help`, and start the default model once.
