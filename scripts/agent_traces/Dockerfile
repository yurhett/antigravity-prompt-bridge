FROM node:24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 ca-certificates ripgrep libsecret-1-0 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/agent-traces
COPY releases.py capture.py protocols.py release.json ./
RUN mkdir -p /opt/client && chown node:node /opt/client
USER node
RUN python3 releases.py install release.json --prefix /opt/client
ENV PYTHONDONTWRITEBYTECODE=1 TRACE_CONTAINER=1
ENTRYPOINT ["python3", "/opt/agent-traces/capture.py", "/opt/agent-traces/release.json", "/opt/client/agent-bin"]
