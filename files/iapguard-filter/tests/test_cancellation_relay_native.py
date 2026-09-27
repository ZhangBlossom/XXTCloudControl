"""Run actual relay on macOS Foundation/StoreKit with fake transaction/observer objects.
No phone, payments or Apple service requests. --red-green also proves an empty relay fails.
"""
from pathlib import Path
import subprocess
import sys
import tempfile

project = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="iapguard-relay-") as work:
    root = Path(work)

    def build(source, name):
        binary = root / name
        subprocess.run([
            "clang++", "-fobjc-arc", "-fmodules", "-Wno-deprecated-declarations",
            f"-fmodules-cache-path={root / 'module-cache'}",
            "-framework", "Foundation", "-framework", "StoreKit",
            "-I", str(project / "src"), str(project / "tests/cancellation_relay_native.mm"),
            str(source), "-o", str(binary),
        ], check=True)
        return binary

    if "--red-green" in sys.argv:
        stub = root / "empty.mm"
        stub.write_text('#import "IAPGuardCancellationRelay.h"\n'
                        'void IAPGuardRelayCancelledPayments(SKPaymentQueue *, NSArray *, NSArray *, id) {}\n')
        failed = subprocess.run([str(build(stub, "red"))], capture_output=True, text=True)
        assert failed.returncode != 0 and "game.calls == 1" in failed.stderr, failed
        print("RED confirmed: empty relay fails the cancellation delivery assertion", flush=True)

    subprocess.run([str(build(project / "src/IAPGuardCancellationRelay.mm", "green"))], check=True)
