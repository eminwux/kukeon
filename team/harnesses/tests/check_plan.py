"""Validate a resolved Buildx Bake plan before any expensive build or publish."""

import json
from pathlib import Path
import sys


def check(plan):
    root = Path(__file__).resolve().parents[2]
    targets = plan["target"]
    expected = {}
    for dockerfile in (root / "harnesses").glob("**/Dockerfile*"):
        name = dockerfile.parent.parent.name if dockerfile.parent.name == "context" else dockerfile.parent.name
        if dockerfile.name != "Dockerfile":
            name += "-" + dockerfile.name.removeprefix("Dockerfile.")
        expected[name] = dockerfile
    assert set(targets) == set(expected), (set(targets), set(expected))
    assert set(plan["group"]["default"]["targets"]) == set(expected)
    for name, target in targets.items():
        dockerfile = root / target["context"] / target["dockerfile"]
        assert dockerfile == expected[name], (name, dockerfile)
        registry, tag = target["args"]["REGISTRY"], target["args"]["TAG"]
        prefix = registry + "/" if registry else ""
        assert target["tags"] == [f"{prefix}{name}:{tag}"]
        assert set(target["platforms"]) == {"linux/amd64", "linux/arm64"}
        assert target["output"] == [{"type": "cacheonly"}], target["output"]
        for line in dockerfile.read_text().splitlines():
            if line.startswith("FROM ${IMAGE_PREFIX}"):
                reference = line.split()[1].replace("${IMAGE_PREFIX}", prefix).replace("${TAG}", tag)
                base = reference.rsplit("/", 1)[-1].split(":")[0]
                assert target["contexts"] == {reference.removesuffix(":latest"): "target:" + base}
    print(f"ok: {len(targets)} images, both architectures, linked bases, no registry outputs")


if __name__ == "__main__":
    check(json.load(sys.stdin))
