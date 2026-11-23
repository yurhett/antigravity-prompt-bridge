"""Select tag baselines and assemble readable Agent traces artifacts."""

import argparse
import hashlib
import html
import io
import json
import os
import re
import shutil
import subprocess
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import quote
from urllib.request import Request, urlopen
from zipfile import BadZipFile, ZIP_DEFLATED, ZipFile

from capture import LANES, canonical
from report import validate

BASELINE_ASSET = "agent-traces-baseline.zip"
MAX_BASELINE = 64 * 1024 * 1024
TAG = re.compile(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)")
LANE_FILES = ("release.json", "trace.json", "report.json", "comparison.json", "stages.json")


def tag_version(tag):
    match = TAG.fullmatch(tag)
    return tuple(map(int, match.groups())) if match else None


def select_baseline_tag(tags, release_tag=""):
    versions = {tag: version for tag in tags if (version := tag_version(tag)) is not None}
    if release_tag:
        if release_tag not in versions:
            raise ValueError("release tag is not a stable tag in this checkout")
        current = versions[release_tag]
        versions = {tag: version for tag, version in versions.items() if version < current}
    return max(versions, key=versions.__getitem__, default="")


def prepare(lane, release_tag):
    tags = subprocess.check_output(["git", "tag", "--list", "v*"], text=True).splitlines()
    tag = select_baseline_tag(tags, release_tag)
    commit = subprocess.check_output(["git", "rev-parse", f"{tag}^{{commit}}"], text=True).strip() if tag else ""
    lanes = list(LANES) if lane == "all" else [lane]
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        print(f"tag={tag}\ncommit={commit}\nlanes={json.dumps(lanes)}", file=output)


def github(path, accept="application/vnd.github+json"):
    request = Request(os.environ["GITHUB_API_URL"] + path, headers={
        "Accept": accept, "X-GitHub-Api-Version": "2022-11-28",
    })
    # Asset downloads may redirect to storage, which must not receive this token.
    request.add_unredirected_header("Authorization", "Bearer " + os.environ["GH_TOKEN"])
    return urlopen(request, timeout=30)


def release_asset(tag):
    repo = os.environ["GITHUB_REPOSITORY"]
    try:
        with github(f"/repos/{repo}/releases/tags/{quote(tag, safe='')}") as response:
            release = json.load(response)
    except HTTPError as exc:
        if exc.code != 404:
            raise
        return None
    return next((asset for asset in release["assets"]
                 if asset["name"] == BASELINE_ASSET and asset["state"] == "uploaded"), None)


def read_baseline(asset, tag, commit, lane):
    repo = os.environ["GITHUB_REPOSITORY"]
    if asset["size"] > MAX_BASELINE:
        raise ValueError("baseline asset exceeds the size limit")
    with github(f"/repos/{repo}/releases/assets/{asset['id']}", "application/octet-stream") as response:
        archive = response.read(MAX_BASELINE + 1)
    if len(archive) > MAX_BASELINE:
        raise ValueError("baseline archive exceeds the size limit")
    with ZipFile(io.BytesIO(archive)) as zipped:
        manifest = json.loads(read_member(zipped, "manifest.json"))
        if manifest["schema"] != 1 or manifest["tag"] != tag or manifest["commit"] != commit:
            raise ValueError("baseline manifest does not match the selected tag and commit")
        info = manifest["lanes"][lane]
        raw = read_member(zipped, f"{lane}/trace.json")
        if hashlib.sha256(raw).hexdigest() != info["trace_sha256"]:
            raise ValueError("baseline trace checksum does not match the manifest")
        trace = json.loads(raw)
        if (trace["release"]["lane"] != lane or trace["release"]["version"] != info["version"]
                or trace["config_revision"] != info["config_revision"]):
            raise ValueError("baseline trace metadata does not match the manifest")
        return trace


def read_member(archive, name):
    member = archive.getinfo(name)
    if member.file_size > MAX_BASELINE:
        raise ValueError("baseline member exceeds the size limit")
    return archive.read(member)


def capture_configuration(trace):
    scenarios = {(item["scenario"], item["protocol"], item["model"]) for item in trace["scenarios"]}
    return trace["config_revision"], scenarios


def compare_with_baseline(current, tag, commit):
    if not tag:
        return {"tag": tag, "status": "missing", "reason": "No historical baseline."}, None
    try:
        asset = release_asset(tag)
        if asset is None:
            return {"tag": tag, "status": "missing", "reason": "No historical baseline for this tag."}, None
        trace = read_baseline(asset, tag, commit, current["release"]["lane"])
        if capture_configuration(trace) != capture_configuration(current):
            return {"tag": tag, "status": "incompatible", "reason": "Capture configurations differ."}, None
        validate(trace)
    except HTTPError as exc:
        return {"tag": tag, "status": "unreadable", "reason": f"Baseline read failed (HTTP {exc.code})."}, None
    except (URLError, OSError, BadZipFile, KeyError, TypeError, ValueError) as exc:
        return {"tag": tag, "status": "unreadable", "reason": f"Baseline read failed ({type(exc).__name__})."}, None
    return {"tag": tag, "status": "available"}, trace


def read_json(folder, name):
    path = folder / name
    return json.loads(path.read_bytes()) if path.is_file() else {}


def write_json(path, value):
    path.write_bytes(canonical(value) + b"\n")


def record_baseline(folder, output, tag, commit):
    comparison, baseline = compare_with_baseline(read_json(folder, "trace.json"), tag, commit)
    if baseline is not None:
        write_json(output, baseline)
    write_json(folder / "comparison.json", comparison)


def inline(value):
    return html.escape(str(value)).replace("|", "&#124;").replace("\n", " ")


def excerpt(value):
    if value == "":
        return "<em>absent</em>"
    escaped = html.escape(str(value)).replace("|", "&#124;")
    escaped = escaped.replace("[", "&#91;").replace("]", "&#93;").replace("`", "&#96;")
    return "<br>".join(f"<code>{line}</code>" for line in escaped.split("\n"))


def lane_summary(folder, lane, baseline_tag):
    release = read_json(folder, "release.json")
    trace = read_json(folder, "trace.json")
    report = read_json(folder, "report.json")
    comparison = read_json(folder, "comparison.json")
    stages = read_json(folder, "stages.json")
    lines = [f"### {lane} · {inline(release.get('version', 'unresolved'))}", ""]

    failed = [stage for stage, status in stages.items() if status == "failure"]
    if failed:
        lines.append("- Failed stages · " + ", ".join(map(inline, failed)))
    if stages.get("capture") == "success" and not comparison:
        lines.append("- Baseline read failed before comparison was recorded.")
    for error in [*trace.get("failures", []), *report.get("failures", [])]:
        lines.append(f"- **{inline(error.get('scenario', 'client'))}** · {inline(error['stage'])} · {inline(error['reason'])}")
    if comparison.get("status") in {"missing", "unreadable", "incompatible"}:
        lines.append(f"- {inline(comparison['reason'])}")
    if not report:
        lines.append("- No replay report available.")
        return lines
    if comparison.get("status") != "available":
        return lines
    tag = inline(comparison.get("tag", baseline_tag))
    version = inline(report["baseline_version"])
    lines.append(f"- Baseline **{tag}** · Agent version **{version}**")
    changes = [item for item in report["diff"] if item.get("old_version") is not None]
    changes.sort(key=lambda item: ("path" in item, not item.get("path", "").endswith("/name")))
    if not changes:
        lines.append("- No changes in the captured request evidence.")
        return lines
    lines.extend(["", "| Change | Before | After |", "| --- | --- | --- |"])
    for item in changes[:12]:
        label = inline(item["scenario"])
        if "round" in item:
            label += f" · round {item['round']}"
        label += " · " + inline(item.get("path", item.get("status", "")))
        before = excerpt(item.get("before", ""))
        after = excerpt(item.get("after", ""))
        lines.append(f"| {label} | {before} | {after} |")
    if len(changes) > 12:
        lines.append("\nAdditional changes are in this lane's report in the download.")
    return lines


def assemble_bundle(inputs, output, lanes, attempt, commit, release_tag, baseline_tag):
    output.mkdir(parents=True, exist_ok=True)
    manifest = {"schema": 1, "commit": commit, "release_tag": release_tag,
                "baseline_tag": baseline_tag, "lanes": {}}
    lines = ["# Agent traces", "", f"Baseline tag · {inline(baseline_tag or 'unavailable')}", ""]
    for lane in lanes:
        prefix = f"agent-trace-lane-{lane}-"
        candidates = {}
        for folder in inputs.glob(prefix + "*"):
            suffix = folder.name.removeprefix(prefix)
            if folder.is_dir() and suffix.isdecimal() and int(suffix) <= attempt:
                candidates[int(suffix)] = folder
        if not candidates:
            manifest["lanes"][lane] = {"status": "missing"}
            lines.extend([f"### {lane}", "", "- No lane artifact available.", ""])
            continue
        source_attempt = max(candidates)
        source = candidates[source_attempt]
        target = output / lane
        target.mkdir(exist_ok=True)
        for name in LANE_FILES:
            if (source / name).is_file():
                shutil.copyfile(source / name, target / name)
        stages = read_json(target, "stages.json")
        report = read_json(target, "report.json")
        status = ("success" if all(stages.get(step) == "success" for step in ("capture", "replay"))
                  and report and not report.get("failures") else "failure")
        manifest["lanes"][lane] = {"status": status, "attempt": source_attempt}
        lines.extend(lane_summary(target, lane, baseline_tag))
        if source_attempt != attempt:
            lines.append(f"- Artifact from run attempt {source_attempt}.")
        lines.append("")
    write_json(output / "manifest.json", manifest)
    (output / "summary.md").write_text("\n".join(lines) + "\n")


def build_baseline(bundle, tag, commit, output):
    source = read_json(bundle, "manifest.json")
    if (source["schema"] != 1 or source["release_tag"] != tag or source["commit"] != commit
            or set(source["lanes"]) != set(LANES)
            or any(lane["status"] != "success" for lane in source["lanes"].values())):
        raise ValueError("release baseline requires successful capture and replay for all lanes at this tag")
    manifest = {"schema": 1, "tag": tag, "commit": commit, "lanes": {}}
    traces = {}
    for lane in LANES:
        raw = (bundle / lane / "trace.json").read_bytes()
        trace = json.loads(raw)
        if trace["release"]["lane"] != lane:
            raise ValueError("trace lane does not match the bundle")
        validate(trace)
        manifest["lanes"][lane] = {
            "version": trace["release"]["version"], "config_revision": trace["config_revision"],
            "trace_sha256": hashlib.sha256(raw).hexdigest(),
        }
        traces[lane] = raw
    with ZipFile(output, "w", compression=ZIP_DEFLATED) as zipped:
        zipped.writestr("manifest.json", canonical(manifest) + b"\n")
        for lane, raw in traces.items():
            zipped.writestr(f"{lane}/trace.json", raw)


def publish_baseline(bundle, tag, commit, output):
    if release_asset(tag):
        print(f"{tag} already has a trace baseline.")
        return
    build_baseline(bundle, tag, commit, output)
    try:
        subprocess.run(["gh", "release", "upload", tag, str(output), "--repo", os.environ["GITHUB_REPOSITORY"]],
                       check=True)
    except subprocess.CalledProcessError:
        # A concurrent retry may have published the asset between our check and upload.
        if release_asset(tag) is None:
            raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    operations = parser.add_subparsers(dest="operation", required=True)
    choose = operations.add_parser("prepare")
    choose.add_argument("--lane", choices=("all", *LANES), default="all")
    choose.add_argument("--release-tag", default="")
    unpack = operations.add_parser("baseline")
    unpack.add_argument("folder", type=Path)
    unpack.add_argument("output", type=Path)
    unpack.add_argument("tag")
    unpack.add_argument("commit")
    stages = operations.add_parser("stages")
    stages.add_argument("folder", type=Path)
    stages.add_argument("results", nargs="+")
    bundle = operations.add_parser("bundle")
    bundle.add_argument("inputs", type=Path)
    bundle.add_argument("output", type=Path)
    bundle.add_argument("--lanes", nargs="+", choices=LANES, required=True)
    bundle.add_argument("--attempt", type=int, required=True)
    bundle.add_argument("--commit", required=True)
    bundle.add_argument("--release-tag", default="")
    bundle.add_argument("--baseline-tag", default="")
    publish = operations.add_parser("publish")
    publish.add_argument("bundle", type=Path)
    publish.add_argument("tag")
    publish.add_argument("commit")
    publish.add_argument("output", type=Path)
    args = parser.parse_args()
    if args.operation == "prepare":
        prepare(args.lane, args.release_tag)
    elif args.operation == "baseline":
        record_baseline(args.folder, args.output, args.tag, args.commit)
    elif args.operation == "stages":
        args.folder.mkdir(parents=True, exist_ok=True)
        write_json(args.folder / "stages.json", dict(result.split("=", 1) for result in args.results))
    elif args.operation == "bundle":
        assemble_bundle(args.inputs, args.output, args.lanes, args.attempt, args.commit, args.release_tag, args.baseline_tag)
    else:
        publish_baseline(args.bundle, args.tag, args.commit, args.output)


if __name__ == "__main__":
    main()
