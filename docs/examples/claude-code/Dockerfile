# Claude Code image used by the run-claude-code guide.
#
# Derived verbatim from eminwux/crew images/claude/Dockerfile so the example
# tracks what is actually running at fleet scale. The companion guide at
# docs/site/guides/run-claude-code.md explains which pieces are kukeon-cell
# essentials (Node + the npm package, the `claude` user) versus crew-fleet
# extras carried over for parity (gh, git signing tooling, golang, jq/yq,
# the `kukeon` gid 988 stanza). Trim freely for your own image — the bits
# the guide's smoke path needs are Node, the `@anthropic-ai/claude-code`
# package, and the non-root `claude` user.

FROM debian:trixie-slim

ARG CLAUDE_CODE_VERSION=latest

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        gnupg \
        nodejs \
        npm \
        make \
        pre-commit \
        ssh \
        golang \
        jq \
        yq \
        gettext-base \
 && install -m 0755 -d /etc/apt/keyrings \
 && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | gpg --dearmor -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends gh \
 && rm -rf /var/lib/apt/lists/*

# Non-root claude user. The `kukeon` gid 988 stanza matches the host
# containerd-socket group on crew's fleet hosts; on a freshly-`kuke init`-ed
# host you don't need it for the smoke path in the companion guide. Left in
# verbatim so this Dockerfile stays usable on a fleet host without edits —
# the guide explains the tradeoff and how to drop it when you don't.
RUN useradd --create-home --shell /bin/bash claude \
 && groupadd -g 988 kukeon \
 && usermod -aG kukeon claude

USER claude
WORKDIR /home/claude

ENV NPM_CONFIG_PREFIX=/home/claude/.npm-global
ENV PATH=/home/claude/.npm-global/bin:$PATH

RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
 && npm cache clean --force \
 && echo 'export NPM_CONFIG_PREFIX=$HOME/.npm-global' >> /home/claude/.bashrc \
 && echo 'export PATH=$HOME/.npm-global/bin:$PATH' >> /home/claude/.bashrc

CMD ["sleep", "infinity"]
