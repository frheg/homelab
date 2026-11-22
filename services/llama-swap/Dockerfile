# llama-swap on a llama.cpp built here, for when the upstream llama-swap image lags llama.cpp (see README).
# Stage 1 is the llama.cpp server image from its own .devops/cuda.Dockerfile (build it first, see README);
# this file adds the llama-swap binary from the upstream image, the same layout as upstream's Containerfile.
ARG LLAMA=llama-cpp-server-cuda:b12619
ARG SWAP=ghcr.io/mostlygeek/llama-swap:v275-cuda-b12602
FROM ${SWAP} AS swap

FROM ${LLAMA}
COPY --from=swap /app/llama-swap /app/llama-swap
ENV PATH="/app:${PATH}"
WORKDIR /app
ENTRYPOINT ["/app/llama-swap"]
