"""Run inside a built mode image; no credentials, daemon, or model calls."""

import os
from pathlib import Path
import pwd
import subprocess
import sys


def run(*command):
    result = subprocess.run(command, text=True, capture_output=True, timeout=30, check=True)
    return result.stdout


def main():
    mode, cli = sys.argv[1:]
    home = Path("/home/user")
    account = pwd.getpwuid(os.geteuid())
    assert account.pw_name == ("root" if mode == "root" else "user"), account
    assert (os.geteuid() == 0) == (mode == "root"), account
    assert os.environ["HOME"] == str(home) == account.pw_dir
    assert Path.home() == home == Path.cwd()
    assert pwd.getpwnam("user").pw_uid != 0
    run("bash", "-lc", 'test "$HOME" = /home/user && test ~ = /home/user')
    for state in (".claude", ".codex", ".local/state", ".cache", ".config", ".android", ".java", ".npm"):
        directory = home / state
        directory.mkdir(parents=True, exist_ok=True)
        probe = directory / "kuketeams-smoke"
        probe.write_text("writable\n")
        assert probe.read_text() == "writable\n"
        probe.unlink()
    config = home / ".ssh/config"
    assert config.stat().st_uid == pwd.getpwnam("user").pw_uid
    try:
        config.write_text("must stay read-only")
    except OSError:
        pass
    else:
        raise AssertionError("SSH fixture must be a read-only mount")
    ssh_args = ["ssh"]
    if mode == "root":
        ssh_args += ["-F", str(config)]
    ssh_args += ["-i", str(home / ".ssh/id_ed25519"),
                 "-o", "StrictHostKeyChecking=accept-new",
                 "-o", f"UserKnownHostsFile={home}/.ssh/known_hosts", "-G", "smoke"]
    ssh = run(*ssh_args)
    assert "hostname fixture.invalid\n" in ssh, ssh
    assert "user fixture-user\n" in ssh, ssh
    assert f"identityfile {home}/.ssh/id_ed25519\n" in ssh, ssh
    assert f"userknownhostsfile {home}/.ssh/known_hosts\n" in ssh, ssh
    if mode == "root":
        for executable in ("docker", "containerd", "iptables", "sudo"):
            run("bash", "-c", 'command -v "$1"', "smoke", executable)
    if cli != "none":
        version = run(cli, "--version")
        assert version.strip(), cli
        run(cli, "--help")
        run("bash", "-lc", 'command -v "$1" && "$1" --version', "smoke", cli)
    print(f"PASS: {account.pw_name} uid={os.geteuid()} home={home}, state, SSH, {cli} startup")


if __name__ == "__main__":
    main()
