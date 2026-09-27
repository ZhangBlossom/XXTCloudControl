#!/bin/sh
set -eu
# Read Debian ar/tar with Python's standard library; dpkg is not needed on macOS.
python3 - "${1:?usage: $0 path/to/package.deb}" <<'PYTHON'
import io
from pathlib import Path
import sys
import tarfile

raw = Path(sys.argv[1]).read_bytes()
assert raw.startswith(b'!<arch>\n'), 'not a Debian ar archive'
position, files = 8, {}
while position < len(raw):
    header = raw[position:position + 60]
    name = header[:16].decode().strip().rstrip('/')
    size = int(header[48:58])
    payload = raw[position + 60:position + 60 + size]
    position += 60 + size + size % 2
    if name.startswith(('control.tar', 'data.tar')):
        with tarfile.open(fileobj=io.BytesIO(payload)) as archive:
            for member in archive.getmembers():
                if member.isfile():
                    files[member.name.removeprefix('./')] = archive.extractfile(member).read()
assert 'Architecture: iphoneos-arm64e' in files['control'].decode(), 'not a RootHide package'
for suffix in ['dylib', 'plist']:
    assert f'Library/MobileSubstrate/DynamicLibraries/zzzIAPGuard.{suffix}' in files
assert not any(name.endswith(('com.iapguard.runtime.plist', 'com.iapguard.allowed-products.plist'))
               for name in files), 'runtime policy must be supplied per order, never packaged'
print('PASS: RootHide plugin/filter package; no installed runtime policy')
PYTHON
