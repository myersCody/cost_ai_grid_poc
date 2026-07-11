FROM mcr.microsoft.com/devcontainers/go:1-1.24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    python3-pip \
    python3-venv \
    libpq-dev \
    postgresql-client \
    jq \
    && rm -rf /var/lib/apt/lists/*

# k3d (runs k3s clusters inside Docker — codespace containers lack systemd for bare k3s)
RUN curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash

# k9s
ARG K9S_VERSION=v0.50.4
RUN curl -sL "https://github.com/derailed/k9s/releases/download/${K9S_VERSION}/k9s_Linux_amd64.tar.gz" \
    | tar xz -C /usr/local/bin k9s \
    && chmod +x /usr/local/bin/k9s

# Python deps for OSAC auth token generation (same as CI)
RUN pip3 install --break-system-packages PyJWT cryptography
