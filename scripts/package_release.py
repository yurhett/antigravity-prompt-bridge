#!/usr/bin/env python3
"""Build a CPA-compatible Linux amd64 archive and checksums.txt."""
import argparse
import hashlib
import re
import struct
import zipfile
from pathlib import Path
from check_version import ROOT, metadata


def package(library, output):
    plugin = metadata()
    data = library.read_bytes()
    assert data[:6] == b"\x7fELF\x02\x01", "expected 64-bit little-endian ELF"
    assert struct.unpack_from("<H", data, 18)[0] == 62, "expected x86-64 ELF"
    glibc_versions = {(int(major), int(minor)) for major, minor in re.findall(rb"GLIBC_(\d+)\.(\d+)", data)}
    assert glibc_versions and max(glibc_versions) <= (2, 34), "GNU libc baseline exceeds 2.34"
    output.mkdir(parents=True, exist_ok=True)
    name = f'{plugin["id"]}_{plugin["version"]}_linux_amd64.zip'
    archive = output / name
    entry = zipfile.ZipInfo(plugin["id"] + ".so", date_time=(2026, 1, 1, 0, 0, 0))
    entry.create_system = 3
    entry.external_attr = 0o100755 << 16
    entry.compress_type = zipfile.ZIP_DEFLATED
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as writer:
        writer.writestr(entry, data)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    (output / "checksums.txt").write_text(f"{digest}  {name}\n")
    print(archive)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--library", type=Path, default=ROOT / "build/agy-coding-filter.so")
    parser.add_argument("--output", type=Path, default=ROOT / "dist")
    args = parser.parse_args()
    package(args.library, args.output)
