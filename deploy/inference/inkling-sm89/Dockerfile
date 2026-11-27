# vLLM nightly + upstream PR vllm-project/vllm#69165 (head 9e0dc2e), so
# Inkling-2-Mini runs on the RTX 6000 Ada (sm_89). Upstream's inkling2
# quantization requires capability 90; the PR lowers it to 80, compiles out PDL
# below SM90 and converts e4m3 in software (the SM89 Triton double-rounding bug,
# vllm-project/vllm#69199). Python/Triton only, so it applies to the installed
# package. Drop this image for the plain vllm/vllm-openai tag once a release
# (>= 0.35.0) carries the PR, and re-run the KB eval before switching.
ARG VLLM_BASE_IMAGE
FROM ${VLLM_BASE_IMAGE}
COPY pr69165.diff /tmp/pr69165.diff
RUN command -v git >/dev/null || (apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/*)
# --check first: a base image the diff no longer fits fails the build here,
# not at model load. Only vllm/*: the PR's tests and recipes are not installed.
RUN cd /usr/local/lib/python3.14/dist-packages \
 && git apply --include='vllm/*' --check -v /tmp/pr69165.diff \
 && git apply --include='vllm/*' /tmp/pr69165.diff \
 && rm /tmp/pr69165.diff
