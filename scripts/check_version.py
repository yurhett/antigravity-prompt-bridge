#!/usr/bin/env python3
"""Require runtime metadata, module, registry and optional tag to agree."""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = "https://github.com/yurhett/cpa-plugin-antigravity-prompt-filter"


def metadata():
    source = (ROOT / "main.go").read_text()
    fields = {name: re.search(r'\b' + name + r'\s*=\s*"([^"]+)"', source).group(1)
              for name in ("pluginName", "pluginVersion", "pluginRepository")}
    plugin = json.loads((ROOT / "registry.json").read_text())["plugins"][0]
    assert fields["pluginName"] == plugin["id"] == "agy-coding-filter"
    assert fields["pluginVersion"] == plugin["version"]
    assert fields["pluginRepository"] == plugin["repository"] == REPOSITORY
    assert (ROOT / "go.mod").read_text().splitlines()[0] == "module " + REPOSITORY.removeprefix("https://")
    assert re.fullmatch(r"\d+\.\d+\.\d+", plugin["version"]), plugin["version"]
    return plugin


if __name__ == "__main__":
    plugin = metadata()
    if len(sys.argv) > 1:
        assert sys.argv[1] == "v" + plugin["version"], "release tag does not match plugin/registry version"
    print(json.dumps({"id": plugin["id"], "version": plugin["version"], "repository": plugin["repository"]}))
