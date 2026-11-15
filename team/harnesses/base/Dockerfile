FROM debian:trixie-slim

ENV DEBIAN_FRONTEND=noninteractive

# Common runtime + dev-loop packages shared by every leaf image:
#   - ca-certificates / curl / gnupg     — fetching upstream artifacts
#   - dnsutils / iputils-ping / netcat-openbsd / telnet
#                                        — in-cell network triage
#   - git / ssh                          — repo clones + push/pull
#   - nodejs / npm                       — agent CLIs install via npm
#   - golang / gcc / libc6-dev           — cgo (e.g. `go test -race`)
#   - python3-pip / python3-venv / python-is-python3
#                                        — Python project test/lint loops
#                                          (pip installs, `python3 -m venv`,
#                                          system hooks invoking `python`)
#   - make / pre-commit                  — project test/lint loops
#   - jq / yq                            — slash-command / blueprint shell glue
# Then layer in GitHub CLI from its own apt repo.

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        dnsutils \
        git \
        gnupg \
        iputils-ping \
        netcat-openbsd \
        nodejs \
        npm \
        make \
        pre-commit \
        ssh \
        telnet \
        golang \
        gcc \
        libc6-dev \
        python3-pip \
        python3-venv \
        python-is-python3 \
        jq \
        yq \
 && install -m 0755 -d /etc/apt/keyrings \
 && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | gpg --dearmor -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends gh \
 && rm -rf /var/lib/apt/lists/*

CMD ["sleep", "infinity"]
