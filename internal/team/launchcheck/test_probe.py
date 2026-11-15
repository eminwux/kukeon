"""Exercise the observed Codex 0.163 instruction/skill catalog encodings."""
from pathlib import Path
import tempfile
import unittest

from probe import discovered_skill, text_parts


class ProbeTests(unittest.TestCase):
    def test_aliased_skill_catalog_resolves_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / 'role/skills/create-cell-config'
            source.mkdir(parents=True)
            (source / 'SKILL.md').write_text('skill')
            installed = root / '.agents/skills/create-cell-config'
            installed.parent.mkdir(parents=True)
            installed.symlink_to(source)
            message = (
                '<skills_instructions>\n## Skills\n### Skill roots\n'
                f'- `r0` = `{installed.parent}`\n'
                '- create-cell-config: Draft config. '
                '(file: r0/create-cell-config/SKILL.md)\n'
            )
            self.assertTrue(discovered_skill([message], source / 'SKILL.md'))
            self.assertFalse(discovered_skill([message], root / 'other/SKILL.md'))

    def test_absolute_path_catalog_and_missing_catalog(self):
        path = Path('/example/create-cell-config/SKILL.md')
        message = f'## Skills\n- create-cell-config: Draft. (file: {path})'
        self.assertTrue(discovered_skill([message], path))
        self.assertFalse(discovered_skill([], path))
        self.assertFalse(discovered_skill(['Read create-cell-config'], path))

    def test_tool_output_json_preserves_actual_read_content(self):
        value = {'output': '{"output":"full skill\\ntext"}'}
        self.assertIn('full skill\ntext', list(text_parts(value)))


if __name__ == '__main__':
    unittest.main()
