"""Run one official CLI against an isolated loopback model endpoint."""

import argparse
import copy
import gzip
import hashlib
import io
import json
import os
import re
import signal
import subprocess
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import unquote, urlsplit

from protocols import (
    ANSWER, CATALOG, RESULT_TOKEN, code_mode, declarations, has_fixture_result, response, sse, system_fields, trace_tool,
)

# Bump when prompts, launch options, or the fixture's contents change.
REVISION = 1
CREDENTIAL = "trace-fixture-dummy-credential"
SIZE_LIMIT = 16 * 1024 * 1024
LANES = {
    "opencode-v1": ("chat", "gpt-4.1"),
    "opencode-v2": ("chat", "gpt-4.1"),
    "pi": ("chat", "gpt-4.1"),
    "codex": ("responses", "gpt-5.4"),
    "gemini": ("gemini", "gemini-2.5-pro"),
    "claude": ("messages", "claude-sonnet-4-6"),
}
SCENARIOS = ("text", "read")
FIELDS = {
    "model", "system", "instructions", "messages", "input", "contents", "tools", "functions",
    "systemInstruction", "system_instruction", "tool_choice", "function_call", "toolConfig", "tool_config",
    "parallel_tool_calls", "stream", "stream_options", "toolChoice", "generationConfig",
}
SECRET_KEYS = {"apikey", "accesstoken", "refreshtoken", "oauthtoken", "authorization", "cookie", "credentials"}
SECRET_PATTERN = re.compile(r"\b(?:sk-(?:proj-)?[\w-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[\w]{25,})\b")
LOCAL_ENDPOINT = re.compile(r"https?://(?:127\.0\.0\.1|localhost)(?::\d+)?")
RUNTIME_DATE = re.compile(r"(?im)^(\s*(?:Today's date|Current date):[ \t]*)[^\r\n]+")


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()


def scrub(value, scratch):
    if isinstance(value, dict):
        # Schema property names are part of the contract. Only redact credential values.
        return {
            key: ("<redacted>" if isinstance(item, str) and re.sub(r"[^a-z]", "", key.lower()) in SECRET_KEYS else scrub(item, scratch))
            for key, item in value.items()
        }
    if isinstance(value, list):
        return [scrub(item, scratch) for item in value]
    if not isinstance(value, str):
        return value
    value = value.replace(CREDENTIAL, "<redacted>")
    for prefix in sorted({str(scratch), os.path.realpath(scratch)}, key=len, reverse=True):
        value = value.replace(prefix, "/trace")
        # Claude's generated memory path embeds a slug of the working directory.
        value = value.replace(re.sub(r"[^A-Za-z0-9]", "-", prefix), "-trace")
    value = RUNTIME_DATE.sub(r"\1<trace-date>", value)
    value = re.sub(r"<current_date>[^<]+</current_date>", "<current_date>trace-date</current_date>", value)
    return SECRET_PATTERN.sub("<redacted>", LOCAL_ENDPOINT.sub("http://127.0.0.1:<port>", value))


def publish(body, scratch):
    retained = scrub({key: value for key, value in body.items() if key in FIELDS}, scratch)
    return {"sha256": hashlib.sha256(canonical(retained)).hexdigest(), "body": retained}


class Collector(ThreadingHTTPServer):
    def __init__(self, protocol, model, fixture, scenario):
        super().__init__(("127.0.0.1", 0), Endpoint)
        self.protocol, self.model, self.fixture, self.scenario = protocol, model, fixture, scenario
        self.lock = threading.Lock()
        self.requests, self.errors = [], []
        self.tool, self.saw_result = None, False
        self.auxiliary = 0

    def next_response(self, body, model=None):
        with self.lock:
            if not list(declarations(body)) and not self.tool:
                self.auxiliary += 1
                if self.auxiliary > 8:
                    raise ValueError("auxiliary request limit exceeded")
                return None
            if len(self.requests) >= 8:
                raise ValueError("request limit exceeded")
            if model is not None and model != self.model:
                raise ValueError("the actual request used a different model than the capture configuration")
            self.requests.append(body)
            if self.scenario == "text" or self.scenario == "code-mode" and not code_mode(self.requests[0]):
                return None
            if self.tool is None:
                self.tool = trace_tool(body, self.fixture, self.scenario)
                return self.tool
            self.saw_result = has_fixture_result(self.protocol, body)
            if not self.saw_result:
                raise ValueError("second model request does not contain the fixture tool result")
            return None


class Endpoint(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send_payload(self, status, payload, content_type="application/json"):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):
        path = urlsplit(self.path).path
        if path.endswith("/models"):
            model = self.server.model
            body = {"object": "list", "data": [{"id": model, "object": "model", "owned_by": "trace", "created": 1}]}
            self.send_payload(200, canonical(body))
            return
        self.send_payload(404, b'{"error":{"message":"unsupported metadata route"}}')

    def do_POST(self):
        path = urlsplit(self.path).path
        if path.endswith(("/count_tokens", ":countTokens")):
            self.send_payload(200, b'{"input_tokens":10,"totalTokens":10}')
            return
        if not path.endswith(("/chat/completions", "/responses", "/messages", ":generateContent", ":streamGenerateContent")):
            self.send_payload(404, b'{"error":{"message":"unsupported model route"}}')
            return
        expected = {"chat": "/chat/completions", "responses": "/responses", "messages": "/messages", "gemini": "GenerateContent"}
        try:
            if not path.lower().endswith(expected[self.server.protocol].lower()):
                raise ValueError("client called a different model protocol")
            length = int(self.headers.get("Content-Length", 0))
            if not 0 < length <= SIZE_LIMIT:
                raise ValueError("request body outside capture size limit")
            raw = self.rfile.read(length)
            if self.headers.get("Content-Encoding") == "gzip":
                with gzip.GzipFile(fileobj=io.BytesIO(raw)) as compressed:
                    raw = compressed.read(SIZE_LIMIT + 1)
                if len(raw) > SIZE_LIMIT:
                    raise ValueError("expanded request body outside capture size limit")
            body = json.loads(raw)
            if not isinstance(body, dict):
                raise ValueError("request body is not a JSON object")
            if self.server.protocol == "gemini":
                model = unquote(path.rsplit("/", 1)[-1].split(":", 1)[0])
            else:
                model = body.get("model")
            call = self.server.next_response(body, model)
            result, events = response(self.server.protocol, model or self.server.model, call)
            if body.get("stream") or path.endswith(":streamGenerateContent"):
                self.send_payload(200, sse(self.server.protocol, events), "text/event-stream")
            else:
                self.send_payload(200, canonical(result))
        except (ValueError, TypeError, KeyError) as exc:
            self.server.errors.append(str(exc))
            self.send_payload(400, b'{"error":{"message":"trace fixture protocol mismatch"}}')


def configure(lane, root, url, model, scenario):
    home = root / "home"
    home.mkdir()
    config = home / ".config"
    env = {
        "PATH": os.environ["PATH"], "HOME": str(home), "XDG_CONFIG_HOME": str(config),
        "XDG_CACHE_HOME": str(home / ".cache"), "XDG_DATA_HOME": str(home / ".local" / "share"),
        "XDG_STATE_HOME": str(home / ".local" / "state"), "TMPDIR": str(root),
        "TERM": "dumb", "LANG": "C.UTF-8", "TZ": "UTC", "CI": "1", "NO_COLOR": "1",
        "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull, "TRACE_API_KEY": CREDENTIAL,
    }
    if lane.startswith("opencode"):
        options = {
            "model": f"trace/{model}", "autoupdate": False, "share": "disabled",
            "provider": {"trace": {"npm": "@ai-sdk/openai-compatible", "name": "Trace",
                                   "options": {"baseURL": url + "/v1", "apiKey": CREDENTIAL},
                                   "models": {model: {"name": model}}}},
            "permission": {"read": "allow", "execute": "allow"},
        }
        env.update({
            "OPENCODE_CONFIG_CONTENT": json.dumps(options), "OPENCODE_DISABLE_AUTOUPDATE": "1",
            "OPENCODE_DISABLE_MODELS_FETCH": "1", "OPENCODE_DISABLE_CLAUDE_CODE": "1",
            "OPENCODE_DISABLE_FILEWATCHER": "1", "OPENCODE_DISABLE_FFF": "1",
        })
        args = ["run", "--model", f"trace/{model}", "--format", "json"]
        if lane == "opencode-v2":
            args.insert(0, "--standalone")
        if scenario == "code-mode":
            env["OPENCODE_FLAGS"] = "full_codemode"
        return env, args
    if lane == "pi":
        directory = home / ".pi" / "agent"
        directory.mkdir(parents=True)
        (directory / "models.json").write_text(json.dumps({
            "providers": {"trace": {"baseUrl": url + "/v1", "api": "openai-completions", "apiKey": "TRACE_API_KEY",
                                    "models": [{"id": model, "name": model}]}}
        }))
        env["PI_CODING_AGENT_DIR"] = str(directory)
        return env, ["--mode", "json", "--print", "--no-session", "--provider", "trace", "--model", model]
    if lane == "codex":
        directory = home / ".codex"
        directory.mkdir()
        (directory / "config.toml").write_text(
            f'model_provider = "trace"\nmodel = "{model}"\napproval_policy = "never"\n'
            f'[model_providers.trace]\nname = "Trace fixture"\nbase_url = "{url}/v1"\n'
            'wire_api = "responses"\nenv_key = "TRACE_API_KEY"\n'
        )
        env["CODEX_HOME"] = str(directory)
        # Docker already constrains egress, mounts and privilege. Its default
        # seccomp profile does not support Codex's nested Linux namespace.
        sandbox = "danger-full-access" if os.environ.get("TRACE_CONTAINER") == "1" else "read-only"
        return env, ["exec", "--json", "--ephemeral", "--sandbox", sandbox]
    if lane == "gemini":
        directory = home / ".gemini"
        directory.mkdir()
        (directory / "settings.json").write_text(json.dumps({
            "security": {"auth": {"selectedType": "gemini-api-key"}}, "telemetry": {"enabled": False},
        }))
        env.update({
            "GEMINI_API_KEY": CREDENTIAL, "GOOGLE_GEMINI_BASE_URL": url, "GEMINI_TELEMETRY_ENABLED": "false",
            "GEMINI_CLI_TRUST_WORKSPACE": "true",
        })
        return env, ["--model", model, "--output-format", "json", "--prompt"]
    env.update({"ANTHROPIC_API_KEY": CREDENTIAL, "ANTHROPIC_BASE_URL": url, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"})
    return env, ["--print", "--output-format", "json", "--permission-mode", "dontAsk", "--model", model, "--allowedTools", "Read", "--"]


def run(binary, args, env, cwd, log, timeout, launcher):
    with log.open("wb") as output:
        process = subprocess.Popen([*launcher, str(binary), *args], env=env, cwd=cwd,
                                   stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            return process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
            raise ValueError("client timed out") from None
        finally:
            # CLI sidecars can outlive their parent. Each capture has its own group.
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass


def prepare_project(project):
    project.mkdir()
    env = {"PATH": os.environ["PATH"], "HOME": str(project.parent), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull}
    subprocess.run(["git", "init", "--quiet", "--initial-branch=main", "--template="], env=env, cwd=project, check=True)
    fixture = project / "trace-example.txt"
    fixture.write_text(RESULT_TOKEN + "\n")
    return fixture.resolve()


def collect(lane, binary, scenario, root, timeout, launcher, debug):
    protocol, model = LANES[lane]
    fixture = prepare_project(root / "project")
    with Collector(protocol, model, fixture, scenario) as server:
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            env, args = configure(lane, root, f"http://127.0.0.1:{server.server_port}", model, scenario)
            task = "Reply with TRACE_DONE." if scenario == "text" else "Read trace-example.txt with the file-reading tool and then reply TRACE_DONE."
            out = root / "client.log"
            rc = run(binary, [*args, task], env, fixture.parent, out, timeout, launcher)
        finally:
            server.shutdown()
            thread.join()
            if debug and out.exists():
                print(scrub(out.read_text(errors="replace")[-12000:], root), file=sys.stderr)
                print("model request shapes:", [
                    {"keys": list(body), "system_fields": [at for at, _ in system_fields(body)],
                     "tools": [tool["name"] for _, _, tool in declarations(body)]}
                    for body in server.requests
                ], "auxiliary requests:", server.auxiliary, file=sys.stderr)
                if server.errors and protocol == "responses" and len(server.requests) > 1:
                    outputs = [item.get("output") for item in server.requests[-1].get("input", [])
                               if item.get("type") == "function_call_output"]
                    print("fixture round trip:", scrub(str(outputs)[:1500], root), file=sys.stderr)
                if server.errors and scenario == "code-mode":
                    for _, text in system_fields(server.requests[0]):
                        entry = CATALOG.search(text)
                        if entry:
                            start = text.rfind("\n#", 0, entry.start()) + 1
                            print("Code Mode catalog:", scrub(text[start:start + 7500], root), file=sys.stderr)
        if server.errors:
            raise ValueError("collector rejected request: " + server.errors[0])
        if not server.requests:
            raise RuntimeError(f"client exited before sending a model request (exit {rc})")
        if rc:
            raise ValueError(f"client did not complete the scenario (exit {rc})")
        if ANSWER not in out.read_text(errors="replace"):
            raise ValueError("client output did not contain the model's completion marker")
        if not list(system_fields(server.requests[0])) or not list(declarations(server.requests[0])):
            raise ValueError("captured request has no system instructions or tool declarations")
        if scenario == "code-mode" and not code_mode(server.requests[0]):
            status = "unsupported"
        elif scenario != "text" and not server.saw_result:
            raise ValueError("client never returned the fixture tool result")
        else:
            status = "captured"
        return {
            "scenario": scenario, "status": status, "protocol": protocol, "model": model,
            "requests": [publish(item, root) for item in server.requests],
            "tool_call": scrub(server.tool, root),
        }


def collect_release(manifest, binary, timeout, launcher=(), debug=False):
    lane = manifest["lane"]
    result = {"schema": 1, "config_revision": REVISION, "release": copy.copy(manifest), "scenarios": [], "failures": []}
    with tempfile.TemporaryDirectory(prefix="agent-trace-version-") as work:
        root = Path(work)
        home = root / "home"
        home.mkdir()
        env = {"PATH": os.environ["PATH"], "HOME": str(home), "XDG_CONFIG_HOME": str(home / ".config"),
               "TMPDIR": str(root), "CI": "1", "OPENCODE_DISABLE_AUTOUPDATE": "1"}
        try:
            log = root / "version.log"
            rc = run(binary, ["--version"], env, root, log, timeout, launcher)
            version = manifest["version"]
            if rc or not re.search(r"(?<![\d.])" + re.escape(version) + r"(?![\d.])", log.read_text(errors="replace")):
                raise ValueError("installed CLI version does not match resolved release")
            result["release"]["running_version"] = version
        except (OSError, ValueError) as exc:
            result["failures"].append({"stage": "startup", "reason": scrub(str(exc), root)})
            return result
    scenarios = (*SCENARIOS, "code-mode") if lane.startswith("opencode") else SCENARIOS
    for scenario in scenarios:
        with tempfile.TemporaryDirectory(prefix="agent-trace-") as work:
            root = Path(work)
            try:
                result["scenarios"].append(collect(lane, binary, scenario, root, timeout, launcher, debug))
            except (OSError, RuntimeError, ValueError, subprocess.CalledProcessError) as exc:
                result["failures"].append({
                    "scenario": scenario, "stage": "startup" if isinstance(exc, (OSError, RuntimeError)) else "capture",
                    "reason": scrub(str(exc), root),
                })
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("binary", type=Path)
    parser.add_argument("result", type=Path)
    parser.add_argument("--timeout", type=int, default=90)
    parser.add_argument("--launcher", action="append", default=[], help="optional local isolation wrapper argument, repeatable")
    parser.add_argument("--debug", action="store_true", help="print sanitized CLI log for local debugging")
    args = parser.parse_args()
    manifest = json.loads(args.manifest.read_text())
    trace = collect_release(manifest, args.binary.resolve(), args.timeout, args.launcher, args.debug)
    payload = canonical(trace) + b"\n"
    if args.result == Path("-"):
        sys.stdout.buffer.write(payload)
    else:
        args.result.write_bytes(payload)
    return bool(trace["failures"])


if __name__ == "__main__":
    sys.exit(main())
