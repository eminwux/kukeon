#!/usr/bin/env python3
"""Request the same single reflection pass as the role's Claude Stop hook."""

import json
import os
import sys


ROLES = {"dev", "pm", "pr-reviewer", "meta-dev", "meta-pm", "meta-reviewer"}


def main():
    try:
        event = json.load(sys.stdin)
        if not isinstance(event, dict):
            raise ValueError("expected a JSON object")
        active = event.get("stop_hook_active", False)
        if not isinstance(active, bool):
            raise ValueError("stop_hook_active must be a boolean")
    except (ValueError, UnicodeError) as error:
        # Hook errors must not create an endless continuation or expose input.
        print(f"reflect-stop: invalid hook input: {error}", file=sys.stderr)
        print("{}")
        return 1

    role = os.environ.get("AGENTS_ROLE", "")
    # Codex 0.163.0 also emits Stop for its ephemeral title-generation session.
    # Blueprint task sessions persist transcripts; internal helpers do not.
    # Explicit --ephemeral runs are consequently outside this hook's scope.
    if active or role not in ROLES or not event.get("transcript_path"):
        print("{}")
        return 0

    condition = (
        "you reproduced a project bug or hit friction"
        if role == "dev"
        else "this task hit friction"
    )
    reason = (
        f"Before finishing, check whether {condition} whose fix names a specific "
        "file and section. If so, invoke /reflect; otherwise skip — most tasks "
        "have nothing to file. Then end the turn."
    )
    print(json.dumps({"decision": "block", "reason": reason}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
