"""Package the viewer built from the same commit as the verification run."""

from pathlib import Path
import hashlib
import json
import os
import platform
import plistlib
import re
import shutil
import subprocess
import xml.etree.ElementTree as ET
import zipfile


ROOT = Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "build" / "verified-viewer"
manifest = ET.parse(ROOT / "viewer/assets/windows.manifest")
identity = manifest.find("{urn:schemas-microsoft-com:asm.v1}assemblyIdentity")
version_parts = identity.attrib["version"].split(".")
if len(version_parts) != 4 or not all(re.fullmatch(r"\d+", p) for p in version_parts):
    raise ValueError("Expected a four-part version in windows.manifest")
VERSION = ".".join(version_parts[:3])
OUTPUT.mkdir(parents=True, exist_ok=True)


def run(args, **env):
    subprocess.run(args, cwd=ROOT / "viewer", env={**os.environ, **env}, check=True)


system = platform.system()
if system == "Windows":
    name = f"CS2-Remote-Viewer-GeForceNOW-x64-{VERSION}"
    executable = OUTPUT / f"{name}.exe"
    run(["go", "build", "-trimpath", "-ldflags", "-s -w -H=windowsgui", "-o", str(executable), "."],
        GOOS="windows", GOARCH="amd64", CGO_ENABLED="0")
    package = OUTPUT / f"{name}.zip"
    with zipfile.ZipFile(package, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.write(executable, f"{name}/{executable.name}")
        archive.write(ROOT / "LICENSE", f"{name}/LICENSE.txt")
        archive.write(ROOT / "viewer/README.md", f"{name}/README.md")
    with zipfile.ZipFile(package) as archive:
        if archive.testzip() is not None:
            raise ValueError("Viewer ZIP verification failed")
elif system == "Darwin":
    staging = ROOT / "build" / "dmg-staging"
    contents = staging / "CS2 Remote Viewer.app" / "Contents"
    (contents / "MacOS").mkdir(parents=True, exist_ok=True)
    (contents / "Resources").mkdir(parents=True, exist_ok=True)
    executable = contents / "MacOS/cs2-remote-viewer"
    run(["go", "build", "-trimpath", "-ldflags", "-s -w", "-o", str(executable), "."],
        GOOS="darwin", GOARCH="arm64", CGO_ENABLED="1")
    plist = {
        "CFBundleIdentifier": "local.research.cs2-remote-viewer",
        "CFBundleName": "CS2 Remote Viewer",
        "CFBundleDisplayName": "CS2 Remote Viewer",
        "CFBundleExecutable": executable.name,
        "CFBundlePackageType": "APPL",
        "CFBundleShortVersionString": VERSION,
        "CFBundleVersion": os.environ.get("GITHUB_RUN_NUMBER", "1"),
        "CFBundleIconFile": "AppIcon.icns",
        "LSMinimumSystemVersion": "13.0",
        "NSHighResolutionCapable": True,
    }
    (contents / "Info.plist").write_bytes(plistlib.dumps(plist))
    shutil.copy2(ROOT / "viewer/assets/AppIcon.icns", contents / "Resources/AppIcon.icns")
    shutil.copy2(ROOT / "LICENSE", staging / "LICENSE.txt")
    shutil.copy2(ROOT / "viewer/README.md", staging / "README.md")
    applications = staging / "Applications"
    if not applications.exists():
        applications.symlink_to("/Applications", target_is_directory=True)
    run(["codesign", "--force", "--sign", "-", str(contents.parent)])
    run(["codesign", "--verify", "--deep", "--strict", str(contents.parent)])
    package = OUTPUT / f"CS2-Remote-Viewer-GeForceNOW-arm64-{VERSION}.dmg"
    run(["hdiutil", "create", "-volname", f"CS2 Remote Viewer {VERSION}", "-srcfolder", str(staging),
         "-format", "UDZO", "-ov", str(package)])
    run(["hdiutil", "verify", str(package)])
else:
    raise ValueError(f"Unsupported viewer packaging platform {system}")

commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
files = [package]
if system == "Windows":
    files.append(executable)
checksums = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
report = {
    "version": VERSION,
    "commit": commit,
    "platform": system,
    "run_id": os.environ.get("GITHUB_RUN_ID"),
    "tests": "Viewer tests and go vet passed before this packaging step",
    "remaining_validation": ["GeForce NOW gameplay alignment, fullscreen and multi-monitor runtime"],
    "signing": "ad-hoc, not notarized" if system == "Darwin" else "unsigned",
    "sha256": checksums,
}
(OUTPUT / f"verification-{system}.json").write_text(json.dumps(report, indent=2) + "\n")
(OUTPUT / f"SHA256SUMS-{system}.txt").write_text(
    "".join(f"{digest}  {name}\n" for name, digest in checksums.items()))
