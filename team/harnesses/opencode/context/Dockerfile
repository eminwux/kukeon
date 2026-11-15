ARG REGISTRY=ghcr.io/eminwux/kuketeams
ARG IMAGE_PREFIX=ghcr.io/eminwux/kuketeams/
ARG TAG=latest
FROM ${IMAGE_PREFIX}base-user:${TAG}

ARG OPENCODE_VERSION=latest

# npm-global install — opencode-ai ships its CLI as an npm package. Land it
# under ~/.local via an npm prefix override so the binary belongs to the
# claude user (the default /usr/lib/node_modules prefix would need root).
# ~/.local is image state (not a bind-mount), so it persists across cell
# restarts without a re-download on first run.
ENV NPM_CONFIG_PREFIX=/home/user/.local
ENV PATH=/home/user/.local/bin:$PATH

RUN npm install -g opencode-ai@"${OPENCODE_VERSION}" \
 && echo 'export PATH=$HOME/.local/bin:$PATH' >> /home/user/.bashrc

# Opencode config is written per-role by the cell's onInit (blueprint
# script) so the `instructions` array can point at the role's playbook.
# OPENROUTER_MODEL / OPENROUTER_API_KEY are filled at run time from the
# cell env via opencode's `{env:NAME}` resolution.

COPY Makefile.start /opt/kuketeams/harness/Makefile.start
