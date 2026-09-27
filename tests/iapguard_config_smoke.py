"""Run actual IAPGuardConfig on macOS with an isolated policy path; no StoreKit or phone."""
from pathlib import Path
import os
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="iapguard-smoke-") as tmp:
    base = Path(tmp)
    (base / "roothide.h").write_text("#import <Foundation/Foundation.h>\nstatic inline NSString *jbroot(NSString *p) { return [NSString stringWithUTF8String:getenv(\"IAPGUARD_TEST_POLICY\")]; }\n")
    binary = base / "check"
    subprocess.run(["xcrun", "clang++", "-fobjc-arc", "-fblocks", "-framework", "Foundation", "-I" + tmp, "-I" + str(root / "files/iapguard-filter/src"), str(root / "tests/iapguard_config_smoke.mm"), str(root / "files/iapguard-filter/src/IAPGuardConfig.mm"), "-o", str(binary)], check=True)
    subprocess.run([str(binary)], env={**os.environ, "IAPGUARD_TEST_POLICY": str(base / "policy.plist")}, check=True)
