"""Real local Go service / HTTP / APK verification. No external business services.

Build the Go binary first. Run with --build only when JDK 17 + SDK are available.
Generated credentials, fixture inputs and outputs default to .cache/verification.
"""
import argparse
import hashlib
import http.client
import io
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import threading
import time
import zipfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / ".cache" / "verification"
V1_BUILD_PATH = "/api/packager/v1/packaging/build"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path)
    parser.add_argument("--output-dir", type=Path, default=OUT)
    parser.add_argument("--build", action="store_true")
    args = parser.parse_args()
    out = args.output_dir.resolve()
    binary = (args.binary or out / "packager.exe").resolve()
    out.mkdir(parents=True, exist_ok=True)
    blob = io.BytesIO()
    with zipfile.ZipFile(blob, "w", zipfile.ZIP_DEFLATED) as z:
        z.writestr("index.html", '<!doctype html><html><body><div id="status">Loading</div><script src="assets/main/game.js"></script></body></html>')
        z.writestr("assets/main/game.js", 'fetch("assets/main/settings.json").then(r=>r.json()).then(s=>{document.getElementById("status").textContent=s.version;window.packConfig=JSON.parse(Android.getPackConfig());});')
        z.writestr("assets/main/settings.json", '{"version":"1.0.1","scene":"verification-fixture"}')
        z.writestr("engine/example.wasm", bytes.fromhex("0061736d01000000"))
    zip_bytes = blob.getvalue()
    (out / "content-v1.zip").write_bytes(zip_bytes)

    class Fixture(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path.startswith("/app/version"):
                body = json.dumps({"version": "1.0.1", "last_version_url": origin + "/content.zip", "sha256": hashlib.sha256(zip_bytes).hexdigest()}).encode()
            elif self.path == "/content.zip":
                body = zip_bytes
            else:
                try:
                    with zipfile.ZipFile(io.BytesIO(zip_bytes)) as z:
                        body = z.read(self.path.split("?", 1)[0].lstrip("/"))
                except KeyError:
                    self.send_error(404)
                    return
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *unused):
            pass

    fixture = ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
    origin = "http://127.0.0.1:" + str(fixture.server_port)
    threading.Thread(target=fixture.serve_forever, daemon=True).start()
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        port = probe.getsockname()[1]
    token = secrets.token_hex(32)
    env = dict(os.environ, PACK_AUTH_TOKEN=token)
    # Parent credentials must not redirect this isolated test to a production file/account.
    for name in list(env):
        if name.startswith("PACK_") and name != "PACK_AUTH_TOKEN":
            del env[name]
    config = {
        "Host": "127.0.0.1", "Port": port, "ProjectRoot": str(ROOT),
        "DataDir": str(out / "service-data"), "BuildTimeoutSec": 3600,
        "VersionPath": "/app/version", "DefaultAppName": "Packapp Verification",
        "Download": {"AllowedOrigins": [origin], "AllowedPrivateCIDRs": ["127.0.0.1/32"], "AllowHTTP": True},
        "Storage": {"Enabled": False}, "AutoGenerateKeystore": False,
    }
    result = {"checks": [], "apks": [], "fixture": "representative nested static files; not a full Cocos game", "androidDevice": "not exercised"}
    if args.build:
        key_dir = out / "keys"
        key_dir.mkdir(exist_ok=True)
        password_file = key_dir / "password"
        if not password_file.exists():
            password_file.write_text(secrets.token_hex(32), encoding="utf-8")
        password = password_file.read_text(encoding="utf-8")
        key = key_dir / "verification.jks"
        env.update(PACK_KEYSTORE_PATH=str(key), PACK_KEYSTORE_ALIAS="packapp-check", PACK_KEYSTORE_STORE_PASS=password, PACK_KEYSTORE_KEY_PASS=password)
        if not key.exists():
            command = ["keytool", "-genkeypair", "-storetype", "JKS", "-keystore", str(key), "-alias", "packapp-check", "-keyalg", "RSA", "-keysize", "2048", "-validity", "3650", "-dname", "CN=Local Verification", "-storepass:env", "PACK_KEYSTORE_STORE_PASS", "-keypass:env", "PACK_KEYSTORE_KEY_PASS"]
            subprocess.run(command, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True, **hidden())
    cfg_path = out / "service.yaml"
    cfg_path.write_text(json.dumps(config, indent=2), encoding="utf-8")
    logs = (out / "service.log").open("w", encoding="utf-8")
    process = subprocess.Popen([binary, "-f", str(cfg_path)], cwd=ROOT, env=env, stdout=logs, stderr=subprocess.STDOUT, **hidden())

    def request(method, path, body=None, auth=True, content_type="application/json", timeout=15):
        c = http.client.HTTPConnection("127.0.0.1", port, timeout=timeout)
        headers = {"Content-Type": content_type}
        if auth:
            headers["X-Pack-Token"] = token
        data = json.dumps(body).encode() if isinstance(body, dict) else body
        try:
            c.request(method, path, data, headers)
            r = c.getresponse()
            return r.status, r.read()
        finally:
            c.close()

    def check(name, expected, method, path, body=None, retry_cleanup=False, **kw):
        status, data = request(method, path, body, **kw)
        if retry_cleanup and status == 429 and (b"already running" in data or b"recovery is pending" in data):
            # Finished ownership can precede the HTTP handler's final log/response cleanup.
            # Respect its Retry-After=10 once; persistent 429 still fails this test.
            print("WAIT bounded cancellation cleanup (one 10s retry)", flush=True)
            result["checks"].append({"name": "bounded cancellation cleanup busy response", "http": 429, "retrySeconds": 10})
            time.sleep(10)
            status, data = request(method, path, body, **kw)
        if status != expected:
            raise AssertionError(f"{name}: expected {expected}, got {status}: {data[:300]!r}")
        result["checks"].append({"name": name, "http": status, "status": "passed"})
        print("PASS", name, status, flush=True)
        return data

    code = 0
    try:
        deadline = time.monotonic() + 15
        while True:
            if process.poll() is not None:
                raise RuntimeError(f"Go service exited; see {out / 'service.log'}")
            try:
                if request("GET", "/health", auth=False)[0] == 200:
                    break
            except OSError:
                pass
            if time.monotonic() > deadline:
                raise TimeoutError("Go service did not become live")
            time.sleep(0.1)
        check("real service liveness", 200, "GET", "/health", auth=False)
        check("admin-style liveness", 200, "GET", "/healthz", auth=False)
        valid = {"url": origin + "/index.html", "dominList": [origin], "version": "1.0.1", "last_version_url": ""}
        check("authentication required", 401, "POST", "/packaging", valid, auth=False)
        check("legacy fields rejected", 400, "POST", "/packaging", {"domain": "legacy"})
        invalid = dict(valid, url="", last_version_url="http://localhost/private.zip")
        check("non-allowlisted ZIP rejected", 400, "POST", "/packaging", invalid)
        check("request size bounded", 413, "POST", "/packaging", b'{"url":"' + b"x" * 20000)
        check("content type enforced", 415, "POST", "/packaging", valid, content_type="text/plain")
        ready_status, ready_body = request("GET", "/ready")
        result["readiness"] = {"http": ready_status, "body": ready_body.decode()}
        if not args.build:
            if ready_status != 503:
                raise AssertionError("Isolated no-signing configuration unexpectedly ready")
            check("URL build reports missing environment", 503, "POST", "/packaging", valid)
            check("local build reports missing environment", 503, "POST", "/packaging", dict(valid, url="", last_version_url=origin + "/content.zip"))
            result["buildStatus"] = "blocked: build dependencies/signing unavailable in this run"
        elif ready_status != 200:
            result["buildStatus"] = "blocked: " + ready_body.decode()
            code = 2
        else:
            # Cancel a real Gradle build after its per-job cache appears, then prove the next
            # request can build. This exercises HTTP disconnect -> worker EOF -> process cleanup.
            jobs_dir = Path(config["DataDir"]) / "jobs"
            before_jobs = set(jobs_dir.iterdir()) if jobs_dir.exists() else set()
            cancel_connection = http.client.HTTPConnection("127.0.0.1", port, timeout=60)
            try:
                cancel_connection.request("POST", "/packaging", json.dumps(valid).encode(), {"Content-Type": "application/json", "X-Pack-Token": token})
                deadline = time.monotonic() + 45
                started = False
                while time.monotonic() < deadline:
                    new_jobs = set(jobs_dir.iterdir()) - before_jobs if jobs_dir.exists() else set()
                    if any((job / "gradle-project-cache" / "8.4").is_dir() for job in new_jobs):
                        started = True
                        break
                    time.sleep(0.05)
                if not started:
                    raise AssertionError("Cancellation fixture did not reach a real Gradle process")
                state_before = json.loads((Path(config["DataDir"]) / "build-owner.json").read_text())
                if state_before["Finished"]:
                    raise AssertionError("Cancellation fixture already finished before disconnect")
                cancel_connection.sock.shutdown(socket.SHUT_RDWR)
            finally:
                cancel_connection.close()
            deadline = time.monotonic() + 20
            finished = False
            while time.monotonic() < deadline:
                try:
                    state = json.loads((Path(config["DataDir"]) / "build-owner.json").read_text())
                    if state["Finished"]:
                        finished = True
                        break
                except (OSError, ValueError, KeyError):
                    pass
                time.sleep(0.05)
            if not finished:
                raise AssertionError("Cancelled build did not release its ownership state")
            cancelled_ids = [job.name for job in new_jobs]
            if any((Path(config["DataDir"]) / "artifacts" / (job_id + ".apk")).exists() for job_id in cancelled_ids):
                raise AssertionError("Cancelled request unexpectedly finished an APK")
            result["checks"].append({"name": "real HTTP build cancellation and automatic cleanup", "status": "passed", "cancelledJobIds": cancelled_ids, "activeBeforeDisconnect": True, "apkProduced": False})
            print("PASS real HTTP build cancellation and automatic cleanup", flush=True)
            for mode in ["url", "local"]:
                payload = dict(valid)
                build_path = "/packaging"
                if mode == "local":
                    payload.update(url="", last_version_url=origin + "/content.zip")
                    build_path = V1_BUILD_PATH
                print("BUILD", mode, "through real POST", build_path, flush=True)
                data = check("real " + mode + " APK build", 200, "POST", build_path, payload, timeout=3700, retry_cleanup=(mode == "url"))
                response = json.loads(data)
                if build_path == V1_BUILD_PATH:
                    if response["code"] != 0 or not response["request_id"] or response["timestamp"] <= 0:
                        raise AssertionError(f"Invalid v1 build response: {response}")
                    response = response["data"]
                    if not response["fileUrl"].startswith("/api/packager/v1/packaging/artifacts/"):
                        raise AssertionError("v1 build returned a different download path")
                artifact = Path(config["DataDir"]) / "artifacts" / response["fileName"]
                with zipfile.ZipFile(artifact) as apk:
                    packed = json.loads(apk.read("assets/pack-config.json"))
                    if packed["url"] != payload["url"] or packed["version"] != "1.0.1":
                        raise AssertionError("APK configuration mismatch")
                    if mode == "local":
                        with zipfile.ZipFile(io.BytesIO(zip_bytes)) as original:
                            for name in original.namelist():
                                if apk.read("assets/html/" + name) != original.read(name):
                                    raise AssertionError("Bundled resource mismatch: " + name)
                    elif any(name.startswith("assets/html/") for name in apk.namelist()):
                        raise AssertionError("URL mode bundled stale HTML")
                check("artifact requires auth", 401, "GET", response["fileUrl"], auth=False)
                downloaded = check("authenticated artifact download", 200, "GET", response["fileUrl"])
                if hashlib.sha256(downloaded).digest() != hashlib.sha256(artifact.read_bytes()).digest():
                    raise AssertionError("Artifact HTTP bytes differ")
                result["apks"].append({"mode": mode, "path": str(artifact), "sha256": hashlib.sha256(downloaded).hexdigest(), "signature": "verified by production apksigner invocation", "signing": "local verification key, not production signing"})
            result["buildStatus"] = "passed: both real signed APK builds and bundled assets verified"
    except Exception as error:
        result["error"] = str(error)
        code = 1
        print("FAIL", error, flush=True)
    finally:
        # 仅停止本脚本创建的服务；服务没有构建子进程存活时才会退出。
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=10)
        logs.close()
        fixture.shutdown()
        result["exitCode"] = code
        (out / "service-result.json").write_text(json.dumps(result, indent=2, ensure_ascii=False), encoding="utf-8")
    return code


def hidden():
    return {"creationflags": subprocess.CREATE_NO_WINDOW} if os.name == "nt" else {}


if __name__ == "__main__":
    sys.exit(main())
