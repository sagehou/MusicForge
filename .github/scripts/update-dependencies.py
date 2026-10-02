"""Resolve stable release metadata in Actions; package resolution stays in Actions too."""
import hashlib
import json
import re
import urllib.parse
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
docker_path = Path("Dockerfile")
docker = re.sub(r"node:[\d.]+-trixie-slim", "node:" + node + "-trixie-slim", docker_path.read_text())
docker = re.sub(r"golang:[\d.]+-trixie", "golang:" + go + "-trixie", docker)
docker_path.write_text(docker)

media_path = Path("build/media-versions.env")
media = dict(line.split("=", 1) for line in media_path.read_text().splitlines() if line and not line.startswith("#"))
ffmpeg_page = fetch("https://ffmpeg.org/download.html").decode()
media["FFMPEG_VERSION"] = re.search(r"releases/ffmpeg-([\d.]+)\.tar\.xz", ffmpeg_page)[1]
opus_page = fetch("https://opus-codec.org/downloads/").decode()
media["OPUS_VERSION"] = re.search(r"opus-([\d.]+)\.tar\.gz", opus_page.split('id="source-code-stable-release"')[1])[1]
urls = {
    "FFMPEG": "https://ffmpeg.org/releases/ffmpeg-{FFMPEG_VERSION}.tar.xz",
    "OPUS": "https://downloads.xiph.org/releases/opus/opus-{OPUS_VERSION}.tar.gz",
    "LAME": "https://downloads.sourceforge.net/project/lame/lame/{LAME_VERSION}/lame-{LAME_VERSION}.tar.gz",
}
# The job records hashes from upstream HTTPS releases once; regular builds verify
# committed hashes. A refresh PR reviews version and hash changes together.
for name, template in urls.items():
    media[name + "_SHA256"] = hashlib.sha256(fetch(template.format(**media))).hexdigest()
media_path.write_text("".join(key + "=" + value + "\n" for key, value in media.items()))
print(json.dumps({"node": node, "go": go, "media": media}, indent=2))

for path in Path(".github/workflows").glob("*.yml"):
    workflow = path.read_text()
    def update_action(match):
        repo = match[1]
        release = metadata("https://api.github.com/repos/" + repo + "/releases/latest")
        tag = release["tag_name"]
        obj = metadata("https://api.github.com/repos/" + repo + "/git/ref/tags/" + tag)["object"]
        while obj["type"] == "tag":
            obj = metadata("https://api.github.com/repos/" + repo + "/git/tags/" + obj["sha"])["object"]
        return "uses: " + repo + "@" + obj["sha"] + " # " + tag
    workflow = re.sub(r"uses: ([\w.-]+/[\w.-]+)@[^\s]+[^\n]*", update_action, workflow)
    path.write_text(workflow)
