"""Resolve stable release metadata in Actions; package resolution stays in Actions too."""
import hashlib
import json
import re
import urllib.parse
import urllib.error
import urllib.request
from pathlib import Path


def fetch(url):
    with urllib.request.urlopen(url, timeout=60) as response:
        return response.read()


def metadata(url):
    return json.loads(fetch(url))


package_path = Path("web/package.json")
package = json.loads(package_path.read_text())
for group in ("dependencies", "devDependencies"):
    for name in package[group]:
        latest = metadata("https://registry.npmjs.org/" + urllib.parse.quote(name, safe="") + "/latest")
        package[group][name] = latest["version"]
package_path.write_text(json.dumps(package, indent=2) + "\n")

node = next(release["version"][1:] for release in metadata("https://nodejs.org/dist/index.json") if not re.search(r"[-+]", release["version"]))
Path(".node-version").write_text(node + "\n")
go = next(release["version"][2:] for release in metadata("https://go.dev/dl/?mode=json") if release["stable"])
module_path = Path("go.mod")
module = re.sub(r"(?m)^go .*", "go " + go, module_path.read_text())
for name in ("github.com/coreos/go-oidc/v3", "golang.org/x/crypto", "golang.org/x/oauth2", "modernc.org/sqlite"):
    version = metadata("https://proxy.golang.org/" + name + "/@latest")["Version"]
    module = re.sub(r"(?m)^(\s*" + re.escape(name) + r" )\S+", lambda match: match[1] + version, module)
module_path.write_text(module)
debian_page = fetch("https://www.debian.org/releases/").decode()
debian = re.search(r"distribution of Debian is version\s+\d+, codenamed <em>([a-z]+)</em>", debian_page)[1]
docker_path = Path("Dockerfile")
original_docker = docker_path.read_text()
adopted = {"node": node, "golang": go}
docker = re.sub(r"node:[\d.]+-[a-z]+-slim", "node:" + node + "-" + debian + "-slim", docker_path.read_text())
docker = re.sub(r"golang:[\d.]+-[a-z]+", "golang:" + go + "-" + debian, docker)
docker = re.sub(r"debian:[a-z]+-slim", "debian:" + debian + "-slim", docker)
# Pin multi-architecture manifest digests so base-image security updates create
# reviewable changes even when the human-readable stable tag stays the same.
def image_digest(image):
    name, tag = image.split(":", 1)
    repo = "library/" + name
    token_url = "https://auth.docker.io/token?" + urllib.parse.urlencode({"service": "registry.docker.io", "scope": "repository:" + repo + ":pull"})
    token = metadata(token_url)["token"]
    request = urllib.request.Request("https://registry-1.docker.io/v2/" + repo + "/manifests/" + tag, method="HEAD", headers={"Authorization": "Bearer " + token, "Accept": "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json"})
    with urllib.request.urlopen(request, timeout=60) as response:
        digest = response.headers["Docker-Content-Digest"]
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise RuntimeError("Invalid base-image manifest digest")
    return digest


def pin_image(match):
    image = match[2]
    name = image.split(":", 1)[0]
    try:
        digest = image_digest(image)
    except urllib.error.HTTPError as error:
        if error.code != 404 or name not in adopted:
            raise
        previous = re.search(r"(?m)^FROM(?: --platform=\S+)? (" + name + r":[^\s@]+)", original_docker)[1]
        if previous == image:
            raise
        print("::warning::" + image + " is not published; retain " + previous + " and continue dependency security updates")
        image = previous
        digest = image_digest(image)
    if name in adopted:
        adopted[name] = image.split(":", 1)[1].split("-", 1)[0]
    return match[1] + image + "@" + digest + match[3]

docker = re.sub(r"(?m)^(FROM(?: --platform=\S+)? )((?:node|golang|debian):[^\s@]+)(?:@sha256:[0-9a-f]+)?([^\n]*)$", pin_image, docker)
docker_path.write_text(docker)
# A language release can precede its official container. Keep CI and Docker aligned.
node, go = adopted["node"], adopted["golang"]
Path(".node-version").write_text(node + "\n")
module_path.write_text(re.sub(r"(?m)^go .*", "go " + go, module_path.read_text()))

media_path = Path("build/media-versions.env")
media = dict(line.split("=", 1) for line in media_path.read_text().splitlines() if line and not line.startswith("#"))
ffmpeg_page = fetch("https://ffmpeg.org/download.html").decode()
media["FFMPEG_VERSION"] = re.search(r"releases/ffmpeg-([\d.]+)\.tar\.xz", ffmpeg_page)[1]
opus_page = fetch("https://opus-codec.org/downloads/").decode()
media["OPUS_VERSION"] = re.search(r"opus-([\d.]+)\.tar\.gz", opus_page.split('id="source-code-stable-release"')[1])[1]
lame_page = fetch("https://lame.sourceforge.io/download.php").decode()
media["LAME_VERSION"] = re.search(r"current release version of LAME is ([\d.]+)", lame_page)[1].rstrip(".")
media["FLAC_VERSION"] = metadata("https://api.github.com/repos/xiph/flac/releases/latest")["tag_name"].removeprefix("v")
urls = {
    "FFMPEG": "https://ffmpeg.org/releases/ffmpeg-{FFMPEG_VERSION}.tar.xz",
    "OPUS": "https://downloads.xiph.org/releases/opus/opus-{OPUS_VERSION}.tar.gz",
    "LAME": "https://downloads.sourceforge.net/project/lame/lame/{LAME_VERSION}/lame-{LAME_VERSION}.tar.gz",
    "FLAC": "https://downloads.xiph.org/releases/flac/flac-{FLAC_VERSION}.tar.xz",
}
# The job records hashes from upstream HTTPS releases once; regular builds verify
# committed hashes. A refresh PR reviews version and hash changes together.
for name, template in urls.items():
    media[name + "_SHA256"] = hashlib.sha256(fetch(template.format(**media))).hexdigest()
media_path.write_text("".join(key + "=" + value + "\n" for key, value in media.items()))
print(json.dumps({"node": node, "go": go, "media": media}, indent=2))

action_cache = {}
for path in Path(".github/workflows").glob("*.yml"):
    workflow = path.read_text()
    def update_action(match):
        repo = match[1]
        if repo in action_cache:
            return action_cache[repo]
        release = metadata("https://api.github.com/repos/" + repo + "/releases/latest")
        tag = release["tag_name"]
        obj = metadata("https://api.github.com/repos/" + repo + "/git/ref/tags/" + tag)["object"]
        while obj["type"] == "tag":
            obj = metadata("https://api.github.com/repos/" + repo + "/git/tags/" + obj["sha"])["object"]
        action_cache[repo] = "uses: " + repo + "@" + obj["sha"] + " # " + tag
        return action_cache[repo]
    workflow = re.sub(r"uses: ([\w.-]+/[\w.-]+)@[^\s]+[^\n]*", update_action, workflow)
    path.write_text(workflow)
