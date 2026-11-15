"""Run inside a built Codex image; no credentials or external model calls.

Checks managed hook discovery and executes a real CLI turn against a local
Responses fixture, proving the Stop hook requests exactly one continuation.
"""

import http.server
import json
import os
from pathlib import Path
import pty
import select
import subprocess
import sys
import tempfile
import termios
import threading
import time


class ResponsesFixture(http.server.BaseHTTPRequestHandler):
    requests = []

    def log_message(self, *_args):
        pass

    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        self.requests.append(json.loads(body))
        index = len(self.requests)
        message = {
            "id": f"msg_{index}", "type": "message", "role": "assistant",
            "status": "completed",
            "content": [{"type": "output_text", "text": "Done.", "annotations": []}],
        }
        events = [
            {"type": "response.created", "response": {
                "id": f"resp_{index}", "status": "in_progress", "output": [],
            }},
            {"type": "response.output_item.done", "output_index": 0, "item": message},
            {"type": "response.completed", "response": {
                "id": f"resp_{index}", "status": "completed", "output": [message],
                "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
            }},
        ]
        data = "".join(
            f"event: {event['type']}\ndata: {json.dumps(event)}\n\n" for event in events
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def task_requests():
    # The TUI also makes an internal request to generate the conversation title.
    return [request for request in ResponsesFixture.requests if any(
        part.get("text") == "Return Done without using tools."
        for item in request.get("input", []) if item.get("role") == "user"
        for part in item.get("content", [])
    )]


def check_discovery(env, cwd):
    requests = [
        {"id": 1, "method": "initialize", "params": {
            "clientInfo": {"name": "reflect-smoke", "version": "1"},
            "capabilities": {"experimentalApi": True},
        }},
        {"method": "initialized"},
        {"id": 2, "method": "hooks/list", "params": {"cwds": [cwd]}},
    ]
    process = subprocess.Popen(
        ["codex", "app-server", "--stdio"], env=env,
        stdin=subprocess.PIPE, stdout=subprocess.PIPE,
        text=True,
    )
    timer = threading.Timer(20, process.kill)
    timer.start()
    try:
        for request in requests:
            process.stdin.write(json.dumps(request) + "\n")
            process.stdin.flush()
            if request.get("id") is None:
                continue
            for line in process.stdout:
                reply = json.loads(line)
                if reply.get("id") == request["id"]:
                    assert "error" not in reply, reply
                    break
            else:
                raise AssertionError("app-server ended before replying")
        entries = reply["result"]["data"]
        assert entries and not entries[0]["errors"], entries
        hooks = [h for h in entries[0]["hooks"] if h["eventName"] == "stop"]
        assert len(hooks) == 1, hooks
        hook = hooks[0]
        assert hook["isManaged"] and hook["enabled"], hook
        assert hook["trustStatus"] == "managed", hook
        assert hook["source"] == "system", hook
        assert hook["sourcePath"] == "/opt/agents/codex/hooks", hook
        print("PASS: managed Stop hook discovered, enabled, and trusted", flush=True)
    finally:
        timer.cancel()
        process.terminate()
        process.wait(timeout=5)


def check_interactive(env, cwd):
    """Drive the real TUI in a PTY without a human or a model service."""
    ResponsesFixture.requests.clear()
    master, slave = pty.openpty()
    termios.tcsetwinsize(slave, (30, 100))
    process = subprocess.Popen(
        ["codex", "--no-daemon", "--no-alt-screen", "--ask-for-approval", "never",
         "--cd", cwd, "--sandbox", "read-only", "Return Done without using tools."],
        env=dict(env, TERM="xterm-256color"), stdin=slave, stdout=slave, stderr=slave,
        start_new_session=True,
    )
    os.close(slave)
    transcript = bytearray()
    deadline = time.monotonic() + 30
    completed_at = None
    try:
        while time.monotonic() < deadline and process.poll() is None:
            ready, _, _ = select.select([master], [], [], 0.1)
            if ready:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                transcript.extend(chunk)
                if b"\x1b[6n" in chunk:
                    os.write(master, b"\x1b[1;1R")
            if len(task_requests()) >= 2:
                if completed_at is None:
                    completed_at = time.monotonic()
                elif time.monotonic() - completed_at > 2:
                    break
        requests = task_requests()
        assert len(requests) == 2, (
            len(requests),
            transcript.decode(errors="replace")[-2000:],
        )
        assert "/reflect" in json.dumps(requests[1]["input"])
        # No reflection continuation may be injected into the title helper.
        helpers = [r for r in ResponsesFixture.requests if r not in requests]
        assert all("<hook_prompt" not in json.dumps(r["input"]) for r in helpers)
        print("PASS: interactive CLI continued exactly once", flush=True)
    finally:
        process.terminate()
        process.wait(timeout=5)
        os.close(master)


def main():
    with tempfile.TemporaryDirectory(prefix="reflect-smoke-", dir=Path.home()) as directory:
        env = dict(os.environ, CODEX_HOME=directory, AGENTS_ROLE="dev")
        check_discovery(env, directory)
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), ResponsesFixture)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        Path(directory, "config.toml").write_text(
            'model = "fixture"\nmodel_provider = "fixture"\n'
            '[model_providers.fixture]\nname = "Fixture"\n'
            f'base_url = "http://127.0.0.1:{server.server_port}/v1"\n'
            'wire_api = "responses"\nrequires_openai_auth = false\n'
            'request_max_retries = 0\nstream_max_retries = 0\n'
            f'[projects.{json.dumps(directory)}]\ntrust_level = "trusted"\n'
        )
        try:
            roles = () if "--interactive-only" in sys.argv else (
                "dev", "pm", "pr-reviewer", "meta-dev", "meta-pm", "meta-reviewer"
            )
            for role in roles:
                env["AGENTS_ROLE"] = role
                ResponsesFixture.requests.clear()
                result = subprocess.run(
                    ["codex", "--ask-for-approval", "never", "exec", "--json",
                     "--skip-git-repo-check", "--cd", directory,
                     "--sandbox", "read-only", "Return Done without using tools."],
                    env=env, capture_output=True, text=True, timeout=30,
                )
                assert result.returncode == 0, (result.stdout, result.stderr)
                requests = ResponsesFixture.requests
                assert len(requests) == 2, (role, len(requests), result.stdout, result.stderr)
                continuation = json.dumps(requests[1]["input"])
                assert "Before finishing, check whether" in continuation, continuation
                assert "/reflect" in continuation, continuation
                print(f"PASS: {role} exec continued exactly once and completed", flush=True)
            check_interactive(env, directory)
        finally:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
