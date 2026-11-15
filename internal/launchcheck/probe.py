"""Read-only evidence collected by the running operator, never credential files."""
import hashlib
import json
import os
import re
from pathlib import Path
import subprocess


def text_parts(value):
    if isinstance(value, str):
        yield value
        try:
            nested = json.loads(value)
        except (ValueError, TypeError):
            return
        if not isinstance(nested, str):
            yield from text_parts(nested)
    elif isinstance(value, list):
        for item in value:
            yield from text_parts(item)
    elif isinstance(value, dict):
        for item in value.values():
            yield from text_parts(item)


def discovered_skill(injected, expected):
    for message in injected:
        roots = dict(re.findall(r"- `([^`]+)` = `([^`]+)`", message))
        for declared in re.findall(r"\(file: ([^)]+/create-cell-config/SKILL\.md)\)", message):
            prefix, _, suffix = declared.partition('/')
            candidate = Path(roots[prefix]) / suffix if prefix in roots else Path(declared)
            if candidate.is_absolute() and candidate.resolve() == expected.resolve():
                return True
    return False


def collect():
    home = Path('/home/user')
    checkout = home / 'kuketeams'
    role = checkout / 'roles/operator'
    codex = home / '.codex'
    skill = home / '.agents/skills/create-cell-config'
    policy_text = (role / 'AGENTS.md').read_text().strip()
    skill_text = (skill / 'SKILL.md').read_text().strip()
    reference_text = (skill / 'references/kukeon.md').read_text().strip()
    # Use only the current thread. Session content stays inside the state volume.
    thread = os.environ.get('CODEX_THREAD_ID')
    records = []
    session_id = ''
    for path in sorted((codex / 'sessions').rglob('*.jsonl'), key=lambda p: p.stat().st_mtime, reverse=True):
        candidate = []
        for line in path.read_text().splitlines():
            try:
                candidate.append(json.loads(line))
            except ValueError:
                continue
        meta = next((r['payload'] for r in candidate if r.get('type') == 'session_meta'), {})
        candidate_id = meta.get('id') or meta.get('session_id', '')
        if (thread and candidate_id == thread) or (not thread and meta.get('cwd') == str(role)):
            records, session_id = candidate, candidate_id
            break
    messages = [r['payload'] for r in records if r.get('type') == 'response_item' and r['payload'].get('role') in ('user', 'developer')]
    # Restrict to injected instruction/catalog messages, excluding the check prompt.
    injected = [s for m in messages for s in text_parts(m.get('content', [])) if s.startswith(('# AGENTS.md instructions', '<skills_instructions>', '## Skills'))]
    tool_output = [s for r in records if r.get('type') == 'response_item' and r['payload'].get('type') in ('function_call_output', 'custom_tool_call_output') for s in text_parts(r['payload'].get('output'))]
    contexts = [r['payload'] for r in records if r.get('type') == 'turn_context']
    config_loaded = any(c.get('approval_policy') == 'never' and c.get('sandbox_policy', {}).get('type') == 'workspace-write' for c in contexts)
    return {
        'thread_id': session_id,
        'source_revision': subprocess.check_output(['git', '-C', str(checkout), 'rev-parse', 'HEAD'], text=True).strip(),
        'codex_version': subprocess.check_output(['codex', '--version'], text=True).strip(),
        'policy_link': (codex / 'AGENTS.md').is_symlink() and (codex / 'AGENTS.md').resolve() == role / 'AGENTS.md',
        'skill_link': skill.is_symlink() and skill.resolve() == role / 'skills/create-cell-config',
        'instructions_loaded': any(policy_text in s for s in injected),
        'skill_discovered': discovered_skill(injected, skill / 'SKILL.md'),
        'skill_read': any(skill_text in s for s in tool_output),
        'reference_read': any(reference_text in s for s in tool_output),
        'config_loaded': config_loaded,
        'codex_home': os.environ.get('CODEX_HOME') == str(codex),
        'working_directory': Path.cwd() == role,
        'requirements_sha256': hashlib.sha256(Path('/etc/codex/requirements.toml').read_bytes()).hexdigest(),
        'hook_sha256': hashlib.sha256(Path('/opt/agents/codex/hooks/reflect-stop.py').read_bytes()).hexdigest(),
    }


if __name__ == '__main__':
    try:
        result = collect()
    except Exception:
        # Never include exception text or arbitrary session/command output.
        result = {'probe_error': True}
    print('KUKETEAMS_PROBE_V1=' + json.dumps(result, sort_keys=True))
