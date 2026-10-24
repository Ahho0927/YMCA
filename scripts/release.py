#!/usr/bin/env python3
"""Build local release archives and checksums; never publishes or includes session HTML."""
import argparse
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import zipfile

parser = argparse.ArgumentParser()
parser.add_argument("--version", default="0.1.0")
args = parser.parse_args()
if not args.version or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-" for c in args.version):
    parser.error("version must contain only letters, numbers, dots, underscores or hyphens")
root = Path(__file__).resolve().parent.parent
dist = root / "dist"
dist.mkdir(exist_ok=True)
checksums = []
for target_os, arch in [("darwin", "amd64"), ("darwin", "arm64"), ("linux", "amd64"), ("linux", "arm64"), ("windows", "amd64")]:
    filename = "ymca.exe" if target_os == "windows" else "ymca"
    archive = dist / f"ymca_{args.version}_{target_os}_{arch}.zip"
    with tempfile.TemporaryDirectory(prefix="ymca-release-") as temporary:
        binary = Path(temporary) / filename
        env = dict(os.environ, GOOS=target_os, GOARCH=arch, CGO_ENABLED="0")
        subprocess.run(["go", "build", "-trimpath", "-ldflags", f"-s -w -X ymca/internal/app.Version={args.version}", "-o", str(binary), "./cmd/ymca"], cwd=root, env=env, check=True)
        with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
            output.write(binary, filename)
            output.write(root / "README.md", "README.md")
        checksums.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}")
    print(archive.name, flush=True)
(dist / "SHA256SUMS").write_text("\n".join(checksums) + "\n")
