#!/usr/bin/env python3
"""Verify the exact archive name, checksum and root library expected by CPA."""
import hashlib
import json
import sys
import zipfile
from pathlib import Path
from check_version import ROOT, metadata


def verify(directory):
    plugin = metadata()
    name = f'{plugin["id"]}_{plugin["version"]}_linux_amd64.zip'
    archive = directory / name
    checksum, recorded_name = (directory / "checksums.txt").read_text().strip().split()
    assert recorded_name == name
    assert hashlib.sha256(archive.read_bytes()).hexdigest() == checksum
    with zipfile.ZipFile(archive) as reader:
        assert reader.namelist() == [plugin["id"] + ".so"], "unexpected archive entries"
        assert reader.testzip() is None
        assert reader.read(reader.namelist()[0]).startswith(b"\x7fELF")
    print(json.dumps({"passed": True, "archive": name, "sha256": checksum}))


if __name__ == "__main__":
    verify(Path(sys.argv[1]) if len(sys.argv) > 1 else ROOT / "dist")
