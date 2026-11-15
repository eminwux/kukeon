"""Run with: python3 -m unittest discover -s harnesses/codex/tests -v."""

import json
import os
from pathlib import Path
import subprocess
import sys
import unittest


ROOT = Path(__file__).resolve().parents[3]
HOOK = ROOT / "harnesses/codex/context/hooks/reflect-stop.py"
ROLES = ("dev", "pm", "pr-reviewer", "meta-dev", "meta-pm", "meta-reviewer")


class ReflectStopTests(unittest.TestCase):
    def run_hook(self, role, payload):
        env = dict(os.environ, AGENTS_ROLE=role)
        try:
            event = json.loads(payload)
            if isinstance(event, dict):
                event.setdefault("transcript_path", "/task/transcript.jsonl")
                payload = json.dumps(event)
        except ValueError:
            pass
        return subprocess.run(
            [sys.executable, str(HOOK)], input=payload, text=True,
            capture_output=True, env=env, check=False,
        )

    def test_first_stop_matches_claude_for_every_role(self):
        for role in ROLES:
            with self.subTest(role=role):
                # Recorded from each upstream role's Claude hook at the copy revision.
                expected = json.loads(
                    (Path(__file__).with_name("claude-stop-fixtures.json")).read_text()
                )[role]
                payload = json.dumps({"stop_hook_active": False})
                codex = self.run_hook(role, payload)
                self.assertEqual(codex.returncode, 0, codex.stderr)
                self.assertEqual(json.loads(codex.stdout), expected)

    def test_continuation_allows_completion_for_every_role(self):
        for role in ROLES:
            with self.subTest(role=role):
                result = self.run_hook(role, '{"stop_hook_active":true}')
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout), {})

    def test_transcript_text_cannot_spoof_the_loop_guard(self):
        payload = json.dumps({
            "stop_hook_active": False,
            "last_assistant_message": 'example: "stop_hook_active": true',
        })
        result = self.run_hook("dev", payload)
        self.assertEqual(json.loads(result.stdout)["decision"], "block")

    def test_missing_flag_is_first_stop(self):
        result = self.run_hook("pm", "{}")
        self.assertEqual(json.loads(result.stdout)["decision"], "block")

    def test_internal_ephemeral_sessions_do_not_reflect(self):
        result = self.run_hook("dev", '{"transcript_path":null,"stop_hook_active":false}')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout), {})

    def test_malformed_input_does_not_request_continuation(self):
        for payload in ("", "not JSON", "null", "[]", '{"stop_hook_active":"false"}'):
            with self.subTest(payload=payload):
                result = self.run_hook("dev", payload)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(json.loads(result.stdout), {})
                self.assertIn("invalid hook input", result.stderr)

    def test_unconfigured_roles_are_not_given_another_roles_policy(self):
        for role in ("", "orchestrator", "unknown"):
            with self.subTest(role=role):
                result = self.run_hook(role, "{}")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout), {})


if __name__ == "__main__":
    unittest.main()
