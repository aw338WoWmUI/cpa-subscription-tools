#!/usr/bin/env python3
"""Exercise the released library through an unmodified CPA host with fake credentials."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument("--host", required=True)
parser.add_argument("--library", required=True)
args = parser.parse_args()
with tempfile.TemporaryDirectory(prefix="subscription-tools-test-") as directory:
    root = Path(directory)
    plugins = root / "plugins"
    plugins.mkdir()
    shutil.copy2(args.library, plugins / Path(args.library).name)
    auths = root / "auths"
    auths.mkdir()
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    config = root / "config.yaml"
    config.write_text(f'''host: "127.0.0.1"
port: {port}
auth-dir: "{auths}"
api-keys: ["synthetic-client-key"]
remote-management:
  allow-remote: true
  secret-key: "synthetic-management-key"
  disable-control-panel: true
plugins:
  enabled: true
  dir: "{plugins}"
  configs:
    cpa-subscription-tools:
      enabled: true
      pacing_enabled: false
''')
    origin = f"http://127.0.0.1:{port}"
    base = "/v0/management/plugins/cpa-subscription-tools"

    def request(path, method="GET", body=None, authenticated=True):
        headers = {"Content-Type": "application/json"}
        if authenticated:
            headers["Authorization"] = "Bearer synthetic-management-key"
        req = urllib.request.Request(origin + path, headers=headers, method=method,
                                     data=None if body is None else json.dumps(body).encode())
        try:
            with urllib.request.urlopen(req, timeout=15) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()

    with (root / "host.log").open("wb") as log:
        process = subprocess.Popen([str(Path(args.host).resolve()), "--config", str(config)],
                                   cwd=root, stdout=log, stderr=subprocess.STDOUT)
        try:
            for _ in range(100):
                if process.poll() is not None:
                    raise RuntimeError("isolated host exited before readiness")
                try:
                    if request("/v8/management/config")[0] == 200:
                        break
                except OSError:
                    pass
                time.sleep(.1)
            else:
                raise RuntimeError("isolated host did not become ready")
            status, body = request("/v0/resource/plugins/cpa-subscription-tools/status", authenticated=False)
            assert status == 200 and b"Subscription Tools" in body, ("resource", status)
            assert request(base + "/accounts", authenticated=False)[0] == 401
            payload = {"provider": "claude", "content": {"mcpOAuth": {"ignore": "mcp-secret"},
                       "claudeAiOauth": {"accessToken": "synthetic-access-secret",
                                         "refreshToken": "synthetic-refresh-secret", "expiresAt": 1893456000000}}}
            status, body = request(base + "/import/preview", "POST", payload)
            assert status == 200 and b"synthetic-access-secret" not in body and b"synthetic-refresh-secret" not in body
            assert not list(auths.glob("*.json")), "preview wrote credentials"
            assert request(base + "/import", "POST", payload)[0] == 400
            status, body = request(base + "/import", "POST", {**payload, "confirm": True})
            assert status == 200, ("import", status, body)
            saved = auths / json.loads(body)["name"]
            content = json.loads(saved.read_text())
            assert content["type"] == "claude" and content["refresh_token"] == "synthetic-refresh-secret"
            assert "mcpOAuth" not in content
            content["refresh_token"] = "rotated-synthetic-refresh-secret"
            saved.write_text(json.dumps(content))
            before = hashlib.sha256(saved.read_bytes()).hexdigest()
            assert request(base + "/import", "POST", {**payload, "confirm": True})[0] == 400
            assert hashlib.sha256(saved.read_bytes()).hexdigest() == before, "duplicate overwrote rotated token"
            status, body = request(base + "/accounts")
            assert status == 200 and b"secret" not in body
            assert request(base + "/reset", "POST", {"confirm": False})[0] == 400
            status, body = request("/v0/management/quota/providers")
            assert status == 200 and b"subscription-tools" in body, ("quota registration", status)
            print("Native host integration passed: ABI, resource, authentication, preview/import, duplicate rotation, redaction, reset confirmation, quota registration.")
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
