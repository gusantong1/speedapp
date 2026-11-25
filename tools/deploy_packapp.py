"""Roll out the prebuilt packapp image on the production Docker host."""

import argparse
from contextlib import contextmanager
import fcntl
import http.client
import json
import os
from pathlib import Path, PurePosixPath
import re
import select
import signal
import stat
import subprocess
import sys
import tempfile
import time


ROOT = Path("/home/ubuntu/.config/golang-game-runtime/packapp-production")
COMPOSE = ROOT / "compose.json"
SERVICE = "packager"
CONTAINER = "gg-client-game-packager-prod"
PORT = 10010


class RolloutError(Exception):
    pass


def docker(*args, timeout=25):
    try:
        result = subprocess.run(
            ["docker", *args], check=False, stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL, timeout=timeout,
        )
    except (OSError, subprocess.TimeoutExpired):
        raise RolloutError(f"Docker {args[0]} could not finish") from None
    if result.returncode != 0:
        raise RolloutError(f"Docker {args[0]} failed (exit {result.returncode})")
    return result.stdout


def inspect(kind, name):
    return json.loads(docker(kind, "inspect", name))[0]


def quiet_node(args, script, *values):
    docker(*args, "node", "-e", script, *values)


MARKERS = """
const fs = require('node:fs');
const crypto = require('node:crypto');
const app = '/app/app';
const icon = fs.readFileSync(app + '/src/main/res/mipmap-xxxhdpi/ic_launcher.webp');
const manifest = fs.readFileSync(app + '/src/main/AndroidManifest.xml', 'utf8');
const gradle = fs.readFileSync(app + '/build.gradle.kts', 'utf8');
const activity = fs.readFileSync(app + '/src/main/java/yq/bxgame/com/X5WebActivity.kt', 'utf8');
const content = fs.readFileSync(app + '/src/main/java/yq/bxgame/com/ContentManager.kt', 'utf8');
if (crypto.createHash('sha256').update(icon).digest('hex') !== process.argv[1] ||
    !manifest.includes('android:roundIcon="@mipmap/ic_launcher"') ||
    !manifest.includes('android:screenOrientation="sensorLandscape"') ||
    !gradle.includes('PACK_APPLICATION_ID') ||
    !activity.includes('getGameApiBaseUrl()') ||
    !content.includes('window.GAME_API_BASE_URL=')) {
    process.exit(1);
}
"""


READY = """
const fs = require('node:fs');
const file = process.env.PACK_AUTH_TOKEN_FILE;
const token = file
    ? fs.readFileSync(file, 'utf8').replace(/[\\r\\n]+$/, '')
    : process.env.PACK_AUTH_TOKEN;
if (!token) process.exit(1);
fetch('http://127.0.0.1:10010/readyz', {
    headers: { 'X-Pack-Token': token },
    redirect: 'error',
    signal: AbortSignal.timeout(6000),
}).then(async response => {
    if (!response.ok || (await response.json()).status !== 'ready') process.exit(1);
}).catch(() => process.exit(1));
"""


def liveness():
    connection = http.client.HTTPConnection("127.0.0.1", PORT, timeout=4)
    try:
        connection.request("GET", "/healthz")
        response = connection.getresponse()
        if response.status != 200 or json.loads(response.read(4096)).get("status") != "ok":
            raise RolloutError("Packapp liveness probe failed")
    except (OSError, http.client.HTTPException, ValueError):
        raise RolloutError("Packapp liveness probe failed") from None
    finally:
        connection.close()


def verify(expected_image, icon=None):
    deadline = time.monotonic() + 70
    while time.monotonic() < deadline:
        try:
            current = inspect("container", CONTAINER)
            if current["Image"] != expected_image or not current["State"]["Running"]:
                raise RolloutError("Production container does not run the expected image")
            liveness()
            quiet_node(["exec", current["Id"]], READY)
            if icon is not None:
                quiet_node(["exec", current["Id"]], MARKERS, icon)
            return current["Id"]
        except RolloutError:
            time.sleep(2)
    raise RolloutError("Production image, liveness or authenticated readiness could not be verified")


def deployment_target():
    current = inspect("container", CONTAINER)
    labels = current["Config"]["Labels"] or {}
    project = labels.get("com.docker.compose.project", "")
    if (labels.get("com.docker.compose.service") != SERVICE
            or labels.get("com.docker.compose.project.config_files") != str(COMPOSE)
            or labels.get("com.docker.compose.project.working_dir") != str(ROOT)
            or not re.fullmatch(r"[a-z0-9][a-z0-9_-]*", project)):
        raise RolloutError("Existing production container is outside the expected Compose project")
    if not current["State"]["Running"]:
        raise RolloutError("Existing production container is not running")
    args = ["compose", "--project-name", project, "--project-directory", str(ROOT), "-f", str(COMPOSE)]
    return current, args


def yaml_path(args):
    for index, value in enumerate(args):
        if value == "-f" and index + 1 < len(args):
            return args[index + 1]
        if value.startswith("-f="):
            return value[3:]
    return "/app/etc/packager.yaml"


def data_directory(container, env):
    if "PACK_DATA_DIR" in env:
        return env["PACK_DATA_DIR"]
    filename = yaml_path(container["Args"])
    if not PurePosixPath(filename).is_absolute():
        raise RolloutError("Packapp configuration filename must be absolute")
    raw = docker("exec", container["Id"], "cat", filename).decode()
    if raw.lstrip().startswith("{"):
        return json.loads(raw).get("DataDir") or "data"
    match = re.search(
        r'(?m)^DataDir:[ \t]*(?:"([^"\r\n]*)"|\'([^\'\r\n]*)\'|([^#\s]+))?[ \t]*(?:#.*)?\r?$',
        raw,
    )
    if not match and re.search(r"(?m)^DataDir:", raw):
        raise RolloutError("Packapp DataDir could not be resolved from runtime configuration")
    return next((part for part in match.groups() if part), "data") if match else "data"


def data_mount(container):
    config = container["Config"]
    env = dict(entry.split("=", 1) for entry in config["Env"])
    path = PurePosixPath(data_directory(container, env) or "data")
    if not path.is_absolute():
        project = env.get("PACK_PROJECT_ROOT", config["WorkingDir"])
        path = PurePosixPath(project) / path
    matches = [mount for mount in container["Mounts"] if mount["Destination"] == str(path)]
    if len(matches) != 1 or not matches[0]["RW"]:
        raise RolloutError("Packapp DataDir does not resolve to one writable persistent mount")
    mount = matches[0]
    kind = mount["Type"]
    if kind not in ("bind", "volume"):
        raise RolloutError("Packapp DataDir mount is not a persistent Docker mount")
    source = mount["Name"] if kind == "volume" else mount["Source"]
    if re.search(r"[,\r\n]", source):
        raise RolloutError("Packapp DataDir contains an unsupported mount path")
    option = ",volume-nocopy" if kind == "volume" else ""
    return f"type={kind},source={source},target=/lockdata{option}"


@contextmanager
def hold_idle_build_lock(container):
    # The Go worker uses flock(LOCK_EX | LOCK_NB) on this same file.
    mount = data_mount(container)
    shell = """
set -eu
umask 077
file=/lockdata/build.lock
[ ! -L "$file" ] && { [ ! -e "$file" ] || [ -f "$file" ]; }
exec 9<>"$file"
flock -n -x 9
[ ! -s "$file" ]
if [ -e /lockdata/build-owner.json ]; then
    grep -Eq '"Finished":[[:space:]]*true' /lockdata/build-owner.json
fi
printf 'locked\\n'
cat >/dev/null
"""
    args = ["docker", "run", "--rm", "-i", "--network", "none", "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges", "--mount", mount, "--entrypoint", "/bin/sh"]
    if container["Config"]["User"]:
        args += ["--user", container["Config"]["User"]]
    args += [container["Image"], "-ec", shell]
    process = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    try:
        readable, _, _ = select.select([process.stdout], [], [], 15)
        if not readable or process.stdout.readline() != b"locked\n":
            raise RolloutError("Packapp has an active or unfinished build, or its build lock was unavailable")
        yield process
    finally:
        process.stdin.close()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        process.stdout.close()


def persist(path, data, old_stat, backup=False):
    fd, name = tempfile.mkstemp(prefix=".packapp-compose-", dir=ROOT)
    temporary = Path(name)
    try:
        with os.fdopen(fd, "wb") as output:
            output.write(data)
            os.fchown(output.fileno(), old_stat.st_uid, old_stat.st_gid)
            os.fchmod(output.fileno(), 0o600 if backup else stat.S_IMODE(old_stat.st_mode))
            output.flush()
            os.fsync(output.fileno())
        if backup:
            os.link(temporary, path)
        else:
            os.replace(temporary, path)
        fd = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        temporary.unlink(missing_ok=True)


def recreate(compose):
    docker(*compose, "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate",
           SERVICE, timeout=100)


def rollout(revision, icon):
    tag = f"golang-game-packapp:production-{revision[:12]}"
    image = inspect("image", tag)
    labels = image["Config"]["Labels"] or {}
    if (labels.get("org.opencontainers.image.revision") != revision
            or image["Os"] != "linux" or image["Architecture"] != "amd64"):
        raise RolloutError("Candidate image revision or platform does not match the deployment")
    docker(
        "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--entrypoint",
        "node", image["Id"], "-e", MARKERS, icon,
    )

    current, compose = deployment_target()
    old_image = current["Image"]
    previous = COMPOSE.read_bytes()
    metadata = COMPOSE.lstat()
    if not stat.S_ISREG(metadata.st_mode):
        raise RolloutError("Existing Compose configuration must be a regular file")
    config = json.loads(previous)
    service = config["services"][SERVICE]
    rendered = json.loads(docker(*compose, "config", "--format", "json"))["services"][SERVICE]
    if rendered["container_name"] != CONTAINER or inspect("image", rendered["image"])["Id"] != old_image:
        raise RolloutError("The Compose file does not describe the running production container")
    if rendered["image"] == tag and old_image == image["Id"]:
        deployed = verify(image["Id"], icon)
        return {"revision": revision, "image": tag, "container": deployed, "alreadyDeployed": True}
    service["image"] = tag
    updated = (json.dumps(config, indent=2, ensure_ascii=False) + "\n").encode()
    backup = ROOT / (f"compose.json.rollback-{time.time_ns()}-{revision[:12]}")

    with hold_idle_build_lock(current) as lease:
        if (COMPOSE.read_bytes() != previous or inspect("container", CONTAINER)["Id"] != current["Id"]
                or lease.poll() is not None):
            raise RolloutError("Production configuration, container or build lock changed during preparation")
        persist(backup, previous, metadata, backup=True)
        try:
            persist(COMPOSE, updated, metadata)
            docker(*compose, "config", "--quiet")
            recreate(compose)
            deployed = verify(image["Id"], icon)
        except (Exception, KeyboardInterrupt):
            persist(COMPOSE, previous, metadata)
            try:
                live = inspect("container", CONTAINER)
            except RolloutError:
                live = None
            if not live or live["Image"] != old_image or not live["State"]["Running"]:
                recreate(compose)
            verify(old_image)
            raise RolloutError("Deployment failed and the previous production image was restored") from None
    return {"revision": revision, "image": tag, "container": deployed, "rollbackConfig": str(backup)}


def interrupted(signum, frame):
    raise RolloutError("Production deployment was interrupted")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("revision", help="40-character source commit")
    parser.add_argument("icon_sha256", help="64-character SHA-256 of the checkout launcher icon")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.revision) or not re.fullmatch(r"[0-9a-f]{64}", args.icon_sha256):
        parser.error("Expected lower-case source commit and launcher icon SHA-256")
    for signum in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
        signal.signal(signum, interrupted)
    try:
        fd = os.open(ROOT / ".packapp-deploy.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "rb") as lock:
            try:
                fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise RolloutError("Another production packapp deployment is running") from None
            result = rollout(args.revision, args.icon_sha256)
            print(json.dumps(result))
    except RolloutError as error:
        print(f"Packapp rollout failed: {error}", file=sys.stderr)
        return 1
    except (OSError, ValueError, KeyError, IndexError, TypeError):
        print("Packapp rollout failed while reading or writing deployment state", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
