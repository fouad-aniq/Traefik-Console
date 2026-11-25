"""Release-1 Docker drills. All projects, volumes, accounts and routes are disposable."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from http.cookiejar import CookieJar

DEPLOY = Path(__file__).resolve().parents[2] / "deploy"
TOKEN = re.compile(r"TCS(?:-[A-Z2-7]{4}){4}")


def command(*args, data=None, env=None):
    # Output can contain CLI credentials. Never include stdout/stderr in failures.
    result = subprocess.run(args, input=data, text=True, capture_output=True, env=env, timeout=180)
    if result.returncode:
        raise RuntimeError(f"{args[0]} {args[1]} failed (exit {result.returncode})")
    return result.stdout.strip()


FULL = "traefik-console-crowdsec"   # traefik-console plus CrowdSec


class Stack:
    """A private copy of deploy/<folder> run under a disposable project name.

    All settings live in the Compose file, so each stack writes this run's values
    (image, random ports, its own user, unique container names) into its copy of
    the file. The new-Traefik folders keep Traefik's files in ./config/traefik and
    ./data, prepared exactly as the file's header tells users to (plain mkdir).
    """
    def __init__(self, image, folder):
        self.project = "tcr1-" + secrets.token_hex(6)
        self.image = image
        self.console_image = image
        self.folder = folder
        self.overrides = []   # extra (text, replacement) pairs, e.g. console-only's Traefik
        self.dir = Path(tempfile.mkdtemp(prefix=self.project + "-"))
        shutil.copytree(DEPLOY / folder, self.dir, dirs_exist_ok=True)
        self.file = self.dir / "docker-compose.yml"
        self.env = dict(os.environ)
        for sub in ("data/console/keys", "data/console/data"):
            (self.dir / sub).mkdir(parents=True, exist_ok=True)
            (self.dir / sub).chmod(0o700)
        if folder != "console-only":
            for sub in ("config/traefik/secrets", "config/traefik/acme", "data/logs", "data/plugins"):
                (self.dir / sub).mkdir(parents=True, exist_ok=True)
            (self.dir / "data/logs/access.log").touch()
        self.write()

    def write(self):
        """Write deploy/<folder>/docker-compose.yml into this copy with this run's values."""
        text = (DEPLOY / self.folder / "docker-compose.yml").read_text()
        sock = Path("/var/run/docker.sock")
        values = [
            ("ghcr.io/fouad-aniq/traefik-console:latest", self.console_image),
            ('"1000:1000"', f'"{os.getuid()}:{os.getgid()}"'),
            ('"999"', f'"{sock.stat().st_gid if sock.exists() else 999}"'),
            ('"80:8000"', '"127.0.0.1::8000"'),
            ('"443:8443"', '"127.0.0.1::8443"'),
            ('"127.0.0.1:8081:8081"', '"127.0.0.1::8081"'),
            ("container_name: traefik ", f"container_name: {self.project}-traefik "),
            ("container_name: crowdsec ", f"container_name: {self.project}-crowdsec "),
            ("TRAEFIK_CONTAINER: traefik ", f"TRAEFIK_CONTAINER: {self.project}-traefik "),
            ("CROWDSEC_CONTAINER: crowdsec ", f"CROWDSEC_CONTAINER: {self.project}-crowdsec "),
        ] + self.overrides
        assert values[0][0] in text, "Console image line not found"
        for old, new in values:
            text = text.replace(old, new)
        self.file.write_text(text)

    def root(self, script):
        # Containers may leave files the test user cannot remove: clean up as root in a throwaway container.
        command("docker", "run", "--rm", "--network", "none", "--user", "0:0", "--entrypoint", "sh",
                "-v", f"{self.dir}:/w", self.image, "-ec", script)

    def use(self, folder):
        # Switch this folder to another new-Traefik setup in place, keeping config/traefik and data.
        extra = DEPLOY / folder / "config" / "crowdsec"
        if extra.exists():
            shutil.copytree(extra, self.dir / "config" / "crowdsec", dirs_exist_ok=True)
        self.folder = folder
        self.write()

    def use_image(self, image):
        self.console_image = image
        self.write()

    def compose(self, *args, data=None):
        return command("docker", "compose", "-p", self.project, "-f", str(self.file), *args, data=data, env=self.env)

    def url(self, service, port):
        addr = self.compose("port", service, str(port))
        host, _, published = addr.rpartition(":")
        assert host in ("127.0.0.1", "0.0.0.0", "[::]", "::"), addr
        return "http://127.0.0.1:" + published

    def config_dir(self):
        return str(self.dir / "config" / "traefik")

    def cleanup(self):
        try:
            self.compose("down", "-v", "--remove-orphans")
        finally:
            try:
                self.root("rm -rf /w/config /w/data")
            finally:
                shutil.rmtree(self.dir, ignore_errors=True)


class Browser:
    def __init__(self, url):
        self.url = url
        self.client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(CookieJar()))
        self.csrf = ""

    def api(self, path, body=None, method="GET", expected=(200,), **headers):
        if self.csrf:
            headers["X-CSRF-Token"] = self.csrf
        headers.update({"Content-Type": "application/json", "Idempotency-Key": secrets.token_hex(16)})
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.url+"/api/v1/"+path, data, headers, method=method)
        try:
            response = self.client.open(req, timeout=60)
        except urllib.error.HTTPError as err:
            response = err
        with response:
            raw = response.read(4*1024*1024)
            if response.status not in expected:
                code = json.loads(raw).get("code", "unknown") if raw.startswith(b"{") else "unknown"
                raise AssertionError(f"{method} {path}: {response.status} {code}")
            return json.loads(raw) if raw else {}


def setup(browser, stack, password):
    browser.api("snapshots", expected=(401,))
    text = stack.compose("exec", "-T", "console", "server", "admin", "setup-token")
    token = TOKEN.search(text).group()
    result = browser.api("auth/setup", {"setup_token":token,"username":"reviewer","password":password}, "POST")
    browser.csrf = result["csrf"]
    browser.api("snapshots")
    assert not TOKEN.search(stack.compose("logs","console"))


def route(browser):
    inputs = [
        ("services/release-console", {"servers":[{"url":"http://console:8081"}]}),
        ("routers/release-route", {"custom_rule":"Path(`/api/v1/health`)","entry_points":["web"],"service":"release-console"}),
    ]
    for path, body in inputs:
        precondition = {"If-None-Match":"*"}
        browser.api(path+"?dry_run=true", body, "PUT", **precondition)
        browser.api(path, body, "PUT", (200,201), **precondition)


def traffic(instance):
    with urllib.request.urlopen(instance.url("traefik",8000)+"/api/v1/health",timeout=10) as response:
        assert json.load(response)["status"] == "ok"


def readiness(stack):
    for _ in range(45):
        try:
            stack.compose("exec","-T","crowdsec","cscli","lapi","status")
            stack.compose("exec","-T","crowdsec","cscli","collections","inspect","crowdsecurity/traefik")
            return
        except RuntimeError:
            time.sleep(2)
    raise AssertionError("CrowdSec LAPI/Traefik collection unavailable")


def roundtrip(image, source, restored):
    # Archive a stopped stack: its named volumes plus its config/traefik and data folders.
    # Restore into empty, separately named volumes and a fresh copy of the folder.
    volumes = ["crowdsec-config","crowdsec-data"]
    backup = source.project+"-backup"
    command("docker","volume","create",backup)
    try:
        for name in volumes:
            dest = restored.project+"_"+name
            command("docker","volume","create","--label","com.docker.compose.project="+restored.project,
                    "--label","com.docker.compose.volume="+name,dest)
            command("docker","run","--rm","--network","none","--user","0:0","--entrypoint","sh",
                    "-v",source.project+"_"+name+":/src:ro","-v",backup+":/backup","-v",dest+":/dest",
                    image,"-ec",'umask 077; tar -cpf /backup/"$1".tar -C /src .; tar -xpf /backup/"$1".tar -C /dest', "sh",name)
        command("docker","run","--rm","--network","none","--user","0:0","--entrypoint","sh",
                "-v",f"{source.dir}:/src:ro","-v",backup+":/backup","-v",f"{restored.dir}:/dest",
                image,"-ec","umask 077; tar -cpf /backup/folders.tar -C /src config/traefik data; "
                "rm -rf /dest/config/traefik /dest/data; tar -xpf /backup/folders.tar -C /dest")
    finally:
        command("docker","volume","rm",backup)


def drill(image, mode, previous):
    # new:    deploy/traefik-console, then switched in place to deploy/traefik-console-crowdsec
    # attach: deploy/console-only next to a deploy/traefik-console-crowdsec fixture
    fixture = Stack(image, FULL) if mode=="attach" else None
    stack = Stack(image, "traefik-console" if mode=="new" else "console-only")
    instance = stack if mode=="new" else fixture
    restored = Stack(image, FULL)
    password = secrets.token_urlsafe(32)
    try:
        if mode=="attach":
            fixture.compose("up","-d","--wait","traefik")
            # Point console-only at the fixture's Traefik: network, config folder, static file, container.
            stack.overrides=[("name: traefik   #",f"name: {fixture.project}_default   #"),
                             ("source: /srv/traefik ",f"source: {fixture.config_dir()} "),
                             ("STATIC_FILE: /config/traefik.yml","STATIC_FILE: /config/static/traefik.yml"),
                             (f"TRAEFIK_CONTAINER: {stack.project}-traefik ",f"TRAEFIK_CONTAINER: {fixture.project}-traefik ")]
            stack.write()
            # The existing observer route must keep responding and its independent file must survive attach.
            before=fixture.compose("exec","-T","traefik","sha256sum","/etc/traefik/dynamic/management.yml")
            probe=stack.compose("run","--rm","--no-deps","--entrypoint","server","console","preflight")
            assert "Preflight passed" in probe
            fixture.compose("exec","-T","traefik","test","!","-e","/etc/traefik/static/console-domains.yml")
        if mode=="new" and previous:
            # Seed a real older database before the reviewed image sees it. Run
            # the old CLI first so its old startup never emits a setup token.
            stack.compose("up","-d","--wait","traefik")
            stack.use_image(previous)
            stack.compose("run","-T","--rm","--no-deps","--entrypoint","server","console","admin","create",
                          "--username","reviewer",data=password+"\n")
            stack.compose("run","-d","--name",stack.project+"-prior","--no-deps","--entrypoint","server","console")
            try:
                for _ in range(30):
                    try:
                        command("docker","exec",stack.project+"-prior","server","healthcheck")
                        break
                    except RuntimeError:
                        time.sleep(1)
                else: raise AssertionError("prior version did not start")
            finally:
                command("docker","rm","-f",stack.project+"-prior")
            stack.use_image(image)
        stack.compose("up","-d","--wait","--wait-timeout","90")
        browser=Browser(stack.url("console",8081))
        if mode=="new" and previous:
            browser.csrf=browser.api("auth/login",{"username":"reviewer","password":password},"POST")["csrf"]
        else:
            setup(browser,stack,password)
        route(browser)
        traffic(instance)
        cid=stack.compose("ps","-q","console")
        mounts=json.loads(command("docker","inspect","--format","{{json .Mounts}}",cid))
        # Only console-only mounts the Docker socket into Console; the all-services file uses the socket-proxy.
        assert any("docker.sock" in entry["Destination"] for entry in mounts) == (stack.folder == "console-only")
        print("PASS",mode,"CrowdSec off, authenticated writes and live route",flush=True)
        if mode=="new":
            stack.use(FULL)   # add CrowdSec in place: same project, volumes and folders
            stack.compose("up","-d","--wait","--wait-timeout","90")
            readiness(stack)
        else:
            fixture.compose("up","-d","--wait","--wait-timeout","90","crowdsec")
            stack.overrides+=[("# CROWDSEC_LAPI_URL: http://crowdsec:8080","CROWDSEC_LAPI_URL: http://crowdsec:8080"),
                              (f"# CROWDSEC_CONTAINER: {stack.project}-crowdsec ",f"CROWDSEC_CONTAINER: {fixture.project}-crowdsec ")]
            stack.write()
            stack.compose("up","-d","--wait","--wait-timeout","90")
            readiness(fixture)
        traffic(instance)
        browser=Browser(stack.url("console",8081))
        browser.csrf=browser.api("auth/login",{"username":"reviewer","password":password},"POST")["csrf"]
        browser.api("routers/release-route")
        if mode=="attach":
            assert fixture.compose("exec","-T","traefik","sha256sum","/etc/traefik/dynamic/management.yml")==before
        print("PASS",mode,"CrowdSec on, LAPI and collection ready, persistence after recreation",flush=True)
        if mode=="new":
            stack.compose("stop")
            roundtrip(image,stack,restored)
            restored.compose("up","-d","--wait","--wait-timeout","90")
            check=Browser(restored.url("console",8081))
            check.csrf=check.api("auth/login",{"username":"reviewer","password":password},"POST")["csrf"]
            check.api("routers/release-route")
            traffic(restored)
            readiness(restored)
            print("PASS fresh-volume offline restore, original login, managed route and LAPI",flush=True)
    finally:
        restored.cleanup()
        stack.cleanup()
        if mode=="attach": fixture.cleanup()


if __name__=="__main__":
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image",required=True)
    parser.add_argument("--previous-image",help="earlier v2 API image, same filesystem layout")
    parser.add_argument("--mode",choices=["new","attach","both"],default="both")
    args=parser.parse_args()
    for mode in (["new","attach"] if args.mode=="both" else [args.mode]):
        drill(args.image,mode,args.previous_image)
