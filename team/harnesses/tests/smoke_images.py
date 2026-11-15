"""Smoke every native mode image using ephemeral containers and an SSH fixture."""

import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def docker(*args):
    subprocess.run(["docker", *args], check=True, timeout=180)


def main():
    registry = os.environ.get("REGISTRY", "ghcr.io/eminwux/kuketeams")
    tag = os.environ.get("TAG", "latest")
    prefix = registry + "/" if registry else ""
    with tempfile.TemporaryDirectory(prefix="kuketeams-ssh-") as directory:
        # Make fixture ownership match the actual image account, including on
        # hosts whose own UID differs. The runtime mounts this directory read-only.
        docker("run", "--rm", "--network", "none", "--user", "root",
               "--mount", f"type=bind,src={directory},dst=/fixture",
               f"{prefix}base-user:{tag}", "bash", "-ec",
               'printf "Host smoke\\n  HostName fixture.invalid\\n  User fixture-user\\n" > /fixture/config; '
               'ssh-keygen -q -t ed25519 -N \"\" -f /fixture/id_ed25519; '
               'touch /fixture/known_hosts; chmod 755 /fixture; chmod 644 /fixture/*; '
               'chmod 600 /fixture/id_ed25519; '
               'chown user:user /fixture/*')
        images = {"base-user": "none", "base-root": "none", "claude": "claude",
                  "claude-root": "claude", "claude-android": "claude", "codex": "codex",
                  "codex-root": "codex", "opencode": "opencode", "opencode-root": "opencode"}
        for image, cli in images.items():
            print(f"Smoke {image}", flush=True)
            mode = "root" if image.endswith("-root") else "user"
            docker("run", "--rm", "--network", "none", "--env", "IS_SANDBOX=1",
                   "--mount", f"type=bind,src={directory},dst=/home/user/.ssh,readonly",
                   "--mount", f"type=bind,src={ROOT / 'harnesses/tests'},dst=/tests,readonly",
                   f"{prefix}{image}:{tag}", "python3", "/tests/smoke_image.py", mode, cli)


if __name__ == "__main__":
    main()
