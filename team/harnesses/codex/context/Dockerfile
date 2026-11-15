ARG REGISTRY=ghcr.io/eminwux/kuketeams
ARG IMAGE_PREFIX=ghcr.io/eminwux/kuketeams/
ARG TAG=latest
FROM ${IMAGE_PREFIX}base-user:${TAG}

ARG CODEX_VERSION=0.163.0

# COPY creates root-owned policy and hook files even in the user image.
COPY requirements.toml /etc/codex/requirements.toml
COPY hooks/reflect-stop.py /opt/agents/codex/hooks/reflect-stop.py

# npm-global install — @openai/codex ships its CLI as an npm package. Land it
# under ~/.local via an npm prefix override so the binary belongs to the
# claude user (the default /usr/lib/node_modules prefix would need root).
# ~/.local is image state (not a bind-mount), so it persists across cell
# restarts without a re-download on first run.
ENV NPM_CONFIG_PREFIX=/home/user/.local
ENV PATH=/home/user/.local/bin:$PATH

RUN npm install -g @openai/codex@"${CODEX_VERSION}" \
 && echo 'export PATH=$HOME/.local/bin:$PATH' >> /home/user/.bashrc

COPY Makefile.start /opt/kuketeams/harness/Makefile.start
