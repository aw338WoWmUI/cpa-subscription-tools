#!/usr/bin/env python3
"""Package the current platform's tested native library for the plugin store."""
import hashlib
import os
from pathlib import Path
import zipfile

plugin = "cpa-subscription-tools"
version = os.environ["RELEASE_VERSION"]
goos = os.environ["GOOS"]
goarch = os.environ["GOARCH"]
extension = {"linux": "so", "darwin": "dylib", "windows": "dll"}[goos]
library = Path("dist") / goos / goarch / f"{plugin}.{extension}"
output = Path("release")
output.mkdir(exist_ok=True)
archive = output / f"{plugin}_{version}_{goos}_{goarch}.zip"
with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as bundle:
    for source in [library, Path("LICENSE"), Path("NOTICE")]:
        bundle.write(source, source.name)
with zipfile.ZipFile(archive) as bundle:
    assert set(bundle.namelist()) == {library.name, "LICENSE", "NOTICE"}
    assert bundle.testzip() is None
digest = hashlib.sha256(archive.read_bytes()).hexdigest()
(output / f"{goos}_{goarch}.sha256").write_text(f"{digest}  {archive.name}\n")
print(archive)
