"""Compile actual Objective-C config and exercise isolated macOS files, never a phone.
Run: python3 files/iapguard-filter/tests/test_config_native.py
"""
from pathlib import Path
import subprocess
import tempfile

project = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="iapguard-native-") as work:
    root = Path(work)
    policy_dir = root / "policy"
    policy_dir.mkdir()
    subprocess.run([
        "clang++", "-fobjc-arc", "-fmodules",
        f"-fmodules-cache-path={root / 'module-cache'}",
        "-framework", "Foundation", "-I", str(project / "src"),
        str(project / "tests/config_native.mm"),
        str(project / "src/IAPGuardConfig.mm"), "-o", str(root / "check"),
    ], check=True)
    try:
        subprocess.run([str(root / "check"), str(policy_dir)], check=True)
    finally:
        policy_dir.chmod(0o700)
