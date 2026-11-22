"""Resolve and install exact upstream Agent releases."""

import argparse
import hashlib
import json
import re
import shutil
import subprocess
import tarfile
import tempfile
from pathlib import Path
from urllib.parse import quote, urlsplit
from urllib.request import Request, urlopen

PACKAGES = {
    "opencode-v1": ("opencode-ai", "opencode"),
    "opencode-v2": (None, "opencode"),
    "pi": ("@earendil-works/pi-coding-agent", "pi"),
    "codex": ("@openai/codex", "codex"),
    "gemini": ("@google/gemini-cli", "gemini"),
    "claude": ("@anthropic-ai/claude-code", "claude"),
}
STABLE = re.compile(r"\d+\.\d+\.\d+")
TAP = "anomalyco/homebrew-tap"


def fetch(url):
    return urlopen(Request(url, headers={"User-Agent": "antigravity-agent-traces"}), timeout=60)


def fetch_json(url):
    with fetch(url) as response:
        return json.load(response)


def open_code_asset(formula):
    version = re.search(r'^\s*version "([^"]+)"', formula, re.MULTILINE)
    asset = re.search(r'^\s*url "(https://opencode\.ai/files/bin/[^"]+/opencode-linux-x64-baseline\.tar\.gz)"\s+sha256 "([a-f0-9]{64})"', formula, re.MULTILINE)
    if not version or not asset or f"/bin/{version[1]}/" not in asset[1]:
        raise ValueError("OpenCode V2 formula has no matching Linux amd64 asset")
    return version[1], asset[1], asset[2]


def resolve(lane):
    package, binary = PACKAGES[lane]
    if package:
        source = f"https://registry.npmjs.org/{quote(package, safe='@')}/latest"
        metadata = fetch_json(source)
        version = metadata["version"]
        asset = metadata["dist"]
        if metadata["name"] != package or metadata.get("deprecated"):
            raise ValueError("unexpected or deprecated npm package")
        url, integrity = asset["tarball"], asset["integrity"]
        if (urlsplit(url).scheme, urlsplit(url).hostname) != ("https", "registry.npmjs.org") or not integrity.startswith("sha512-"):
            raise ValueError("unexpected npm asset or integrity")
        proof = {"source": source, "url": url, "integrity": integrity, "package": package}
    else:
        ref = fetch_json(f"https://api.github.com/repos/{TAP}/git/ref/heads/master")["object"]["sha"]
        if not re.fullmatch(r"[a-f0-9]{40}", ref):
            raise ValueError("unexpected OpenCode tap revision")
        source = f"https://raw.githubusercontent.com/{TAP}/{ref}/opencode-v2.rb"
        with fetch(source) as response:
            version, url, digest = open_code_asset(response.read().decode())
        proof = {"source": source, "url": url, "sha256": digest, "commit": ref}
    if not STABLE.fullmatch(version):
        raise ValueError(f"expected a stable version, got {version}")
    return {"lane": lane, "binary": binary, "version": version, **proof}


def install(manifest, prefix):
    prefix.mkdir(parents=True, exist_ok=True)
    package = manifest.get("package")
    if package:
        subprocess.run([
            "npm", "install", "--no-audit", "--no-fund",
            "--save-exact", f"{package}@{manifest['version']}",
        ], cwd=prefix, check=True)
        entry = json.loads((prefix / "package-lock.json").read_text())["packages"][f"node_modules/{package}"]
        if entry["version"] != manifest["version"] or entry["integrity"] != manifest["integrity"]:
            raise ValueError("installed npm package does not match release metadata")
        return prefix / "node_modules" / ".bin" / manifest["binary"]
    with tempfile.TemporaryFile() as archive:
        with fetch(manifest["url"]) as response:
            shutil.copyfileobj(response, archive)
        archive.seek(0)
        if hashlib.file_digest(archive, "sha256").hexdigest() != manifest["sha256"]:
            raise ValueError("OpenCode V2 archive SHA-256 mismatch")
        archive.seek(0)
        with tarfile.open(fileobj=archive, mode="r:gz") as tar:
            files = [m for m in tar.getmembers() if m.isfile() and Path(m.name).name == "opencode"]
            if len(files) != 1:
                raise ValueError("expected one regular OpenCode executable in the archive")
            binary = prefix / "opencode"
            with tar.extractfile(files[0]) as src, binary.open("wb") as dst:
                shutil.copyfileobj(src, dst)
    binary.chmod(0o755)
    return binary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("resolve", "install"))
    parser.add_argument("manifest", type=Path)
    parser.add_argument("--lane", choices=PACKAGES)
    parser.add_argument("--prefix", type=Path)
    args = parser.parse_args()
    if args.operation == "resolve":
        if args.lane is None:
            parser.error("resolve requires --lane")
        args.manifest.write_text(json.dumps(resolve(args.lane), indent=2) + "\n")
    else:
        if args.prefix is None:
            parser.error("install requires --prefix")
        manifest = json.loads(args.manifest.read_text())
        binary = install(manifest, args.prefix)
        (args.prefix / "agent-bin").symlink_to(binary.resolve())


if __name__ == "__main__":
    main()
