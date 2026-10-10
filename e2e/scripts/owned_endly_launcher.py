#!/usr/bin/env python3
"""Run the approved terminal-waiting MCP launcher and always clean owned services.

Use this around the MCP client that records an operation handle and waits for its
terminal status. A queued-only launcher is not an acceptance run. This wrapper
never discovers/kills processes by port and never creates/resets databases.
"""
import os
import pathlib
import subprocess
import sys

PORTS = (29501, 29502, 29511, 29512, 29513, 29514, 29515, 29516)


def cleanup_owned_services(run=subprocess.run):
    helper = pathlib.Path(__file__).with_name('owned_endly_process.py')
    failures = []
    for port in PORTS:
        result = run([sys.executable, str(helper), 'stop', str(port)], check=False)
        if result.returncode:
            failures.append(port)
    return failures


def launch(command, run=subprocess.run, cleanup=cleanup_owned_services):
    if not command or not os.environ.get('CORE_ENDLY_ROOT'):
        raise ValueError('approved MCP launcher and explicit owned fixture root required')
    status = 70
    cleanup_failures = []
    try:
        status = run(command, check=False).returncode
    finally:
        # An Endly assertion/action failure skips its final workflow task. The
        # external owner therefore closes every recorded service regardless of
        # the launcher's exit status or exception, attempting every owned port.
        cleanup_failures = cleanup()
    if cleanup_failures:
        print('owned cleanup requires review for ports: ' + ','.join(map(str, cleanup_failures)), file=sys.stderr)
        return 71
    return status


if __name__ == '__main__':
    command = sys.argv[1:]
    if command[:1] == ['--']:
        command = command[1:]
    try:
        sys.exit(launch(command))
    except (ValueError, OSError):
        print('approved owned MCP launcher failed', file=sys.stderr)
        sys.exit(70)
