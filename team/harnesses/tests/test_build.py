"""Exercise real Make recursion, generated Bake graphs and release tooling."""

import json
import os
import re
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
CATALOGS = sorted((ROOT / "harnesses").glob("*/images.yaml"))


class BuildTests(unittest.TestCase):
    def run_build(self, mode, platforms="linux/amd64,linux/arm64", *args,
                  harness=None, registry="registry.test/team", make=False):
        with tempfile.TemporaryDirectory() as tmp:
            docker = Path(tmp) / "docker"
            docker.write_text('''#!/usr/bin/env python3
import json,sys
from pathlib import Path
args=sys.argv[1:]
plan=json.loads(Path(args[args.index("--file")+1]).read_text())
print(json.dumps({"args":args,"plan":plan}))
''')
            docker.chmod(0o755)
            command = [str(ROOT / "harnesses/build-images.sh"), *args]
            if make:
                command = ["make", "--no-print-directory", "-s", "-C",
                           str(ROOT / "harnesses" / (harness or "")), "images",
                           f"REGISTRY={registry}", "TAG=v7.8.9", f"MODE={mode}",
                           f"PLATFORMS={platforms}", f"IMAGES={' '.join(args)}"]
            return subprocess.run(
                command,
                env=dict(os.environ, PATH=tmp + os.pathsep + os.environ["PATH"],
                         MODE=mode, PLATFORMS=platforms, REGISTRY=registry,
                         TAG="v7.8.9"),
                cwd=tmp, text=True, capture_output=True, check=False,
            )

    def test_validation_has_no_export(self):
        result = self.run_build("check")
        self.assertEqual(result.returncode, 0, result.stderr)
        record = json.loads(result.stdout)
        self.assertEqual(record["args"][:2], ["buildx", "bake"])
        self.assertNotIn("--set", record["args"])
        self.assertEqual(len(record["plan"]["group"]["default"]["targets"]), 10)

    def test_load_is_single_platform_and_explicit(self):
        self.assertEqual(self.run_build("load").returncode, 2)
        result = self.run_build("load", "linux/amd64", "codex")
        self.assertEqual(result.returncode, 0, result.stderr)
        record = json.loads(result.stdout)
        self.assertEqual(record["args"][-2:], ["--set", "*.output=type=docker"])
        self.assertEqual(record["plan"]["group"]["default"]["targets"], ["codex"])
        self.assertEqual(record["plan"]["target"]["codex"]["contexts"],
                         {"registry.test/team/base-user:v7.8.9": "target:base-user"})

    def test_publication_is_explicit(self):
        result = self.run_build("push")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["args"][-2:], ["--set", "*.output=type=registry"])
        self.assertEqual(self.run_build("typo").returncode, 2)
        self.assertNotEqual(self.run_build("check", "linux/amd64", "typo").returncode, 0)

    def test_make_delegation_and_arguments(self):
        before = {p: p.read_bytes() for p in CATALOGS}
        result = self.run_build("push", make=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        records = [json.loads(line) for line in result.stdout.splitlines()]
        self.assertEqual([r["plan"]["group"]["default"]["targets"] for r in records], [
            ["base", "base-user", "base-root"],
            ["claude", "claude-android", "claude-root"],
            ["codex", "codex-root"], ["opencode", "opencode-root"]])
        for record in records:
            self.assertEqual(record["args"][-1], "*.output=type=registry")
            for name, target in record["plan"]["target"].items():
                self.assertEqual(target["tags"], [f"registry.test/team/{name}:v7.8.9"])
                self.assertEqual(target["args"]["REGISTRY"], "registry.test/team")
                self.assertEqual(target["platforms"], ["linux/amd64", "linux/arm64"])
        self.assertEqual({p: p.read_bytes() for p in CATALOGS}, before)

    def test_direct_and_aggregate_local_subset(self):
        before = {p: p.read_bytes() for p in CATALOGS}
        for harness in (None, "codex"):
            result = self.run_build("load", "linux/amd64", "codex-root",
                                    make=True, harness=harness, registry="")
            self.assertEqual(result.returncode, 0, result.stderr)
            records = [json.loads(line) for line in result.stdout.splitlines()]
            self.assertEqual(len(records), 1)
            record = records[0]
            self.assertEqual(record["plan"]["group"]["default"]["targets"], ["codex-root"])
            target = record["plan"]["target"]["codex-root"]
            self.assertEqual(target["tags"], ["codex-root:v7.8.9"])
            self.assertEqual(target["args"]["REGISTRY"], "")
            self.assertEqual(target["args"]["IMAGE_PREFIX"], "")
            self.assertEqual(target["contexts"], {"base-root:v7.8.9": "target:base-root"})
        self.assertEqual({p: p.read_bytes() for p in CATALOGS}, before)

    def test_direct_build_rejects_foreign_subset(self):
        result = self.run_build("check", "linux/amd64", "claude", make=True, harness="codex")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("No selected images belong", result.stderr)

    def test_release_check_is_read_only_and_rejects_mismatch(self):
        script = ROOT / "harnesses/release-images.sh"
        before = {p: p.read_bytes() for p in CATALOGS}
        current = re.search(rb"image:\s+[^\s]+:(v[^\s]+)", next(iter(before.values()))).group(1).decode()
        for tag, expected in ((current, 0), ("v99.0.0", 1), ("latest", 2)):
            result = subprocess.run([str(script), tag, "--check"], capture_output=True, check=False,
                                    env=dict(os.environ, REGISTRY="ghcr.io/eminwux/kukeon"))
            self.assertEqual(result.returncode, expected, result.stderr)
            self.assertEqual({p: p.read_bytes() for p in CATALOGS}, before)
            if expected == 1:
                for path in CATALOGS:
                    self.assertIn(str(path.relative_to(ROOT)).encode(), result.stderr)
                self.assertIn(b"commit/merge", result.stderr)

    def test_workflow_checks_before_publication(self):
        workflow = (ROOT.parent / ".github/workflows/release.yaml").read_text()
        job = workflow[workflow.index("  harness_images:"):]
        self.assertEqual(job.count('./team/harnesses/release-images.sh "$GITHUB_REF_NAME" --check'), 1)
        self.assertLess(job.index("setup-go"), job.index("release-images.sh"))
        self.assertLess(job.index("release-images.sh"), job.index("docker/login-action"))
        self.assertIn('make -C team/harnesses images REGISTRY="$REGISTRY" TAG="$TAG" PLATFORMS="$PLATFORMS" MODE=push', job)
        self.assertIn("REGISTRY: ghcr.io/eminwux/kukeon", job)
        self.assertIn("TAG: ${{ github.ref_name }}", job)
        self.assertIn("PLATFORMS: linux/amd64,linux/arm64", job)

if __name__ == "__main__":
    unittest.main()
