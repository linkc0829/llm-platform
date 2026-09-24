# Deploying on the AI Station

This covers restart protection and health probes on the Ubuntu host (Phase 0-A A6). The current Windows host is not covered here.

| Process | Runs under | Restarts when |
| --- | --- | --- |
| vLLM | Docker, [`vllm/compose.yaml`](vllm/compose.yaml) | the container exits (`restart: unless-stopped`) |
| gateway | systemd, [`systemd/gateway.service`](systemd/gateway.service) | the process crashes or exits non-zero (`Restart=on-failure`) |
| kb | systemd, [`systemd/kb.service`](systemd/kb.service) | the process crashes or exits non-zero |

Docker itself is started by systemd, so all three come back after a reboot.

## Install

```bash
# vLLM
cd deploy/vllm && cp .env.example .env   # pin the image tag, set the model
docker compose up -d

# gateway + kb: binaries, .env, auth.json, docs/ and .kb/ under /opt/llm-platform
sudo useradd --system --home /opt/llm-platform llm-platform
make build && sudo cp bin/gateway bin/kb /opt/llm-platform/bin/
sudo cp deploy/systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now gateway kb
```

Point the gateway at `GATEWAY_UPSTREAM_BASE_URL=http://127.0.0.1:8000`. vLLM listens on loopback only because it has no per-user authentication, so all traffic must go through the gateway.

## Probes

Liveness asks whether the process is up. Readiness asks whether it can answer right now.

| Endpoint | Kind | 200 means | 503 means |
| --- | --- | --- | --- |
| kb `GET /health` | liveness | process up (always 200; also reports model and vectors) | — |
| kb `GET /ready` | readiness | index loaded and vectors `ok` (or deliberately `disabled`) | `not_indexed`, or `stale`: retrieval has fallen back to BM25 |
| gateway `GET /healthz` | liveness | process up | — |
| gateway `GET /readyz` | readiness | the chat upstream answered `GET /v1/models` within 2 s | upstream down, still loading, or hung. The cause is logged as `gateway_not_ready`, not returned |
| vLLM `GET /health` | container healthcheck | engine up | reported as `unhealthy` in `docker ps` after the 10-minute load grace period |

For B2 (MTTR), kill vLLM and time how long the gateway's `/readyz` takes to return 200 again.

## Known limits

- **systemd restarts only a process that has exited.** A gateway or kb that hangs without exiting stays down. If B2 shows this happens, add `WatchdogSec=` plus an `sd_notify` heartbeat in the binary.
- **Docker does not restart an `unhealthy` container.** It restarts only on exit. A vLLM engine that wedges while its process is still running needs a human, or an autoheal sidecar if B2 shows it is needed.
- **The restart behaviour has not been exercised yet.** That is B2, which needs the machine.
