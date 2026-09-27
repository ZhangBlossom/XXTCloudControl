"""Capture only IAPGuard diagnostic events from an attached test iPhone."""
import argparse
import os
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tool", required=True, help="Path to pymobiledevice3")
    parser.add_argument("--output", required=True)
    parser.add_argument("--seconds", type=int, default=360)
    args = parser.parse_args()
    if not 1 <= args.seconds <= 900:
        parser.error("seconds must be between 1 and 900")
    # Exclusive creation prevents overwriting an earlier diagnostic run.
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as output:
        process = subprocess.Popen(
            [args.tool, "syslog", "live", "--match", "[IAPGuardDiag]"],
            stdout=output, stderr=subprocess.PIPE,
            env={**os.environ, "NO_COLOR": "1", "PYTHONUNBUFFERED": "1"},
        )
        try:
            _, errors = process.communicate(timeout=args.seconds)
        except subprocess.TimeoutExpired:
            process.terminate()
            try:
                _, errors = process.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                _, errors = process.communicate()
        else:
            if process.returncode:
                raise SystemExit(errors.decode(errors="replace"))
    print(f"Diagnostic capture saved: {args.output}")


if __name__ == "__main__":
    main()
