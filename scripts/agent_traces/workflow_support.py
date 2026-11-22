"""Find the latest successful capture and render a small Actions summary."""

import argparse
import html
import io
import json
import os
import sys
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
from zipfile import ZipFile

MAX_BASELINE = 64 * 1024 * 1024


def github(path):
    request = Request(os.environ["GITHUB_API_URL"] + path, headers={
        "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28",
    })
    # The archive endpoint redirects to storage. Keep the API token on the first hop.
    request.add_unredirected_header("Authorization", "Bearer " + os.environ["GH_TOKEN"])
    return urlopen(request, timeout=30)


def github_json(path):
    with github(path) as response:
        return json.load(response)


def previous_baseline(lane, repo, branch, run_id):
    query = urlencode({"name": f"agent-trace-baseline-{lane}", "per_page": 100})
    artifacts = github_json(f"/repos/{repo}/actions/artifacts?{query}")["artifacts"]
    for artifact in sorted(artifacts, key=lambda obj: obj["created_at"], reverse=True):
        run = artifact["workflow_run"]
        if artifact["expired"] or run is None or str(run["id"]) == run_id or run["head_branch"] != branch:
            continue
        try:
            run_info = github_json(f"/repos/{repo}/actions/runs/{run['id']}")
            if run_info["path"] != ".github/workflows/agent-traces.yml" or run_info["status"] != "completed":
                continue
            if artifact["size_in_bytes"] > MAX_BASELINE:
                raise ValueError("baseline artifact exceeds size limit")
            with github(f"/repos/{repo}/actions/artifacts/{artifact['id']}/zip") as response:
                archive = response.read(MAX_BASELINE + 1)
        except HTTPError as exc:
            # An artifact can expire between listing and downloading it.
            if exc.code in (404, 410):
                continue
            raise
        if len(archive) > MAX_BASELINE:
            raise ValueError("baseline archive exceeds size limit")
        with ZipFile(io.BytesIO(archive)) as zipped:
            member = zipped.getinfo("trace.json")
            if member.file_size > MAX_BASELINE:
                raise ValueError("baseline trace exceeds size limit")
            return zipped.read(member)
    return None


def read_json(folder, name, fallback):
    path = folder / name
    return json.loads(path.read_text()) if path.is_file() else fallback


def summary(folder, lane, stages):
    release = read_json(folder, "release.json", {})
    trace = read_json(folder, "trace.json", {})
    report = read_json(folder, "report.json", {})
    version = html.escape(str(release.get("version", "unresolved")))
    lines = [f"### {lane} · {version}", ""]
    if report:
        baseline = report["baseline_version"]
        lines.append(f"- Compared to version {html.escape(baseline)}." if baseline else "- First sample for this capture configuration.")
        for replay in report["replays"]:
            rounds = replay["rounds"]
            families = ", ".join(sorted({family for r in rounds for family in r["agent_families"]})) or "unidentified"
            template_hits = sum(r["template_hits"] for r in rounds)
            residual_fixed = sum(r["residual_fixed"] for r in rounds)
            aliases = sum(len(r["aliases"]) + len(r["code_mode_aliases"]) for r in rounds)
            lines.append(f"- **{replay['scenario']}** {replay['status']} · {len(rounds)} request(s) · {html.escape(families)} · "
                         f"{template_hits} template hits · {residual_fixed} residual fixes · {aliases} tool aliases")
        changes = [item for item in report["diff"] if "path" in item and item["old_version"] is not None]
        candidates = sum(len(item.get("candidates", [])) for item in report["diff"])
        lines.append(f"- {len(changes)} evidence change(s), {candidates} candidate rule(s). See the lane artifact for details.")
        for failure in report["failures"]:
            lines.append(f"- **Replay failure** {html.escape(failure['scenario'])}, request {failure['round']} · {html.escape(failure['reason'])}")
    else:
        for label, state in stages.items():
            if state == "failure":
                lines.append(f"- Failed during **{label}**.")
        for failure in trace.get("failures", []):
            lines.append(f"- {html.escape(failure.get('scenario', 'client'))} {html.escape(failure['stage'])} · {html.escape(failure['reason'])}")
    return "\n".join(lines) + "\n\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("baseline", "summary"))
    parser.add_argument("folder", type=Path)
    parser.add_argument("lane")
    parser.add_argument("stages", nargs="*")
    args = parser.parse_args()
    if args.operation == "baseline":
        baseline = previous_baseline(args.lane, os.environ["GITHUB_REPOSITORY"],
                                     os.environ["TRACE_DEFAULT_BRANCH"], os.environ["GITHUB_RUN_ID"])
        if baseline is not None:
            (args.folder / "baseline.json").write_bytes(baseline)
    else:
        lines = summary(args.folder, args.lane, dict(s.split("=", 1) for s in args.stages))
        sys.stdout.write(lines)


if __name__ == "__main__":
    main()
