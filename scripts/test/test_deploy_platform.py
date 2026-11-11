"""deploy.sh's machine_platform: which build a machine gets (#3103).

Runs the function as it is in scripts/deploy/deploy.sh with `field` and `is_here` stubbed and a PATH that holds only
stub `uname` / `ssh` commands, so it behaves the same on macOS and Linux.
"""
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
DEPLOY = ROOT / 'scripts/deploy/deploy.sh'
BASH = shutil.which('bash') or '/bin/bash'

UNAME = '''#!%s
printf '%%s\\n' "$*" >> "$CALLS"
case "$1" in
  -s) printf '%%s\\n' %s ;;
  -m) printf '%%s\\n' %s ;;
esac
'''

SSH = '''#!%s
printf 'ssh %%s\\n' "$*" >> "$CALLS"
[ -n "${REMOTE:-}" ] || exit 255
printf '%%s\\n' "$REMOTE"
'''


def machine_platform_source():
    match = re.search(r'^machine_platform\(\) \{\n.*?^\}\n', DEPLOY.read_text(), re.S | re.M)
    assert match, 'machine_platform not found in deploy.sh'
    return match.group(0)


def quote(value):
    return "'" + value.replace("'", "'\\''") + "'"


class MachinePlatformTests(unittest.TestCase):
    def platform(self, os_name='', arch='', remote='', local=None):
        """os_name / arch: what the config says ('' = the field is not written); remote: `uname -s` and `uname -m`
        the machine answers over ssh, one per line ('' = it cannot be reached); local: the same for this computer,
        None = it is not this computer."""
        with tempfile.TemporaryDirectory() as directory:
            stubs = Path(directory)
            calls = stubs / 'calls.log'
            calls.write_text('')
            stubs_scripts = {'ssh': SSH % BASH}
            if local is not None:
                stubs_scripts['uname'] = UNAME % (BASH, quote(local[0]), quote(local[1]))
            for name, body in stubs_scripts.items():
                (stubs / name).write_text(body)
                (stubs / name).chmod(0o755)
            here = 'true' if local is not None else 'false'
            script = '''field() { case "$1:$2" in
  m:os) printf %s @OS@ ;;
  m:arch) printf %s @ARCH@ ;;
  m:ssh) printf 'box1' ;;
esac; }
is_here() { @HERE@; }
@SOURCE@
machine_platform m
'''.replace('@OS@', quote(os_name)).replace('@ARCH@', quote(arch)) \
   .replace('@HERE@', here).replace('@SOURCE@', machine_platform_source())
            result = subprocess.run([BASH, '-c', script], capture_output=True, text=True, timeout=10,
                                    env={'PATH': str(stubs), 'CALLS': str(calls), 'REMOTE': remote})
            return result, calls.read_text()

    def test_an_intel_mac_gets_the_amd64_build(self):
        # The config can only say `os`: before this, darwin always got the arm64 build.
        result, _ = self.platform('darwin', remote='Darwin\nx86_64')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'darwin-amd64')

    def test_an_apple_silicon_mac_gets_the_arm64_build(self):
        result, _ = self.platform('darwin', remote='Darwin\narm64')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'darwin-arm64')

    def test_a_machine_without_os_is_asked_which_it_runs(self):
        # A Linux machine whose config entry has no `os` used to get darwin-arm64.
        result, _ = self.platform(remote='Linux\nx86_64')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'linux-amd64')

    def test_arm_linux_gets_its_own_build(self):
        result, _ = self.platform('linux', remote='Linux\naarch64')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'linux-arm64')

    def test_a_configured_platform_is_not_asked_again(self):
        result, calls = self.platform('darwin', 'arm64')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'darwin-arm64')
        self.assertEqual(calls.strip(), '')

    def test_this_computer_answers_without_ssh(self):
        result, calls = self.platform(local=('Darwin', 'arm64'))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'darwin-arm64')
        self.assertNotIn('ssh ', calls)

    def test_a_platform_without_a_build_is_an_error_not_a_guess(self):
        result, _ = self.platform('windows', 'amd64')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('no personad build for windows/amd64', result.stderr)

    def test_a_machine_that_cannot_be_asked_is_unreachable_not_a_platform(self):
        # 3, not 1: a laptop that is asleep is a normal deploy (`unreachable, skipped`), not a platform the hub has no
        # build for, and must not fail the run (#3103).
        result, _ = self.platform()
        self.assertEqual(result.returncode, 3, result.stderr)
        self.assertIn('could not reach it to ask its os and arch', result.stderr)

    def test_a_configured_os_without_arch_offline_is_unreachable_too(self):
        # Every config written before `arch` existed: the machine still has to be asked for it, and being offline is
        # not a platform problem.
        result, _ = self.platform('darwin')
        self.assertEqual(result.returncode, 3, result.stderr)

    def test_an_answer_that_is_not_a_platform_is_an_error(self):
        result, _ = self.platform(remote='FreeBSD\nriscv64')
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn('could not tell its os and arch', result.stderr)


class DeployBuildsEveryPlatformTests(unittest.TestCase):
    def test_the_deploy_and_the_hub_carry_all_four_platforms(self):
        # "Redeploy" takes the node binary from the hub's bin/nodes, so the hub needs every platform a node can run.
        text = DEPLOY.read_text()
        self.assertIn('PLATFORMS=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64)', text)
        self.assertIn('for plat in "${PLATFORMS[@]}"; do\n  GOOS=${plat%-*} GOARCH=${plat#*-}', text)
        self.assertIn('for plat in "${PLATFORMS[@]}"; do\n        remote_state "$PERSONA_HUB_SSH"', text)
        # persona-node is built for every platform too, beside personad; the hub gets it whole, its old version file
        # gone first and the new one written last.
        self.assertIn('-o "bin/persona-node-$plat" ./cmd/persona-node', text)
        # Both binaries land whole: the old version file goes first, the binary is staged, then renamed over its final path.
        self.assertIn('n="$PERSONA_HOME/bin/nodes/\'"personad-$plat"\'"; rm -f "$n.version"; cat > "$n.new" && mv "$n.new" "$n"\' < "bin/personad-$plat"', text)
        self.assertIn('rm -f "$n.version"; cat > "$n.new" && mv "$n.new" "$n"\' < "bin/persona-node-$plat"\n'
                      '        printf \'%s\\n\' "$VERSION" | remote_state "$PERSONA_HUB_SSH" \'cat > "$PERSONA_HOME/bin/nodes/\'"persona-node-$plat.version"', text)

    def test_the_hub_and_the_nodes_do_not_pick_a_build_from_os_alone(self):
        text = DEPLOY.read_text()
        self.assertNotIn('[ "$os" = darwin ] && echo arm64', text)
        self.assertIn('hub_plat=$(machine_platform "$t")', text)
        self.assertIn('plat=$(machine_platform "$t"); then', text)
        # An unreachable machine is still skipped without failing the run; only a platform without a build fails it.
        self.assertIn('echo "$t: unreachable, skipped" >&2', text)


if __name__ == '__main__':
    unittest.main()
