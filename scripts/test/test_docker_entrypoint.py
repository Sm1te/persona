from pathlib import Path
import json
import os
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
DOCKER = ROOT / "deploy/docker"
ENTRYPOINT = DOCKER / "entrypoint.sh"
WRAPPER = DOCKER / "personad.sh"
COMPOSE = DOCKER / "compose.yaml"
# Real tools the scripts use besides the stubs (the image's busybox has them all).
TOOLS = ("mkdir", "mv", "grep", "cat")


def link_tools(root):
    for name in TOOLS:
        os.symlink(shutil.which(name), root / name)


def write_stubs(root, commands):
    for name, content in commands.items():
        path = root / name
        path.write_text(content)
        path.chmod(0o755)


class DockerEntrypointTests(unittest.TestCase):
    def run_entrypoint(self, desktop, access="", cidrs="", exit_code=0, hub_json=None, public_url=""):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            home = root / "home"
            home.mkdir()
            commands = {
                "id": '#!/bin/sh\ncase "$1" in -u) echo 1000 ;; -un) echo persona ;; *) exit 1 ;; esac\n',
                "getent": '#!/bin/sh\n[ "$1 $2" = "hosts host.docker.internal" ] || exit 2\nexit ' + ("0" if desktop else "1") + '\n',
                "personad": '#!/bin/sh\nprintf "%s\\n" "$@"\nexit ' + str(exit_code) + '\n',
            }
            write_stubs(bin_dir, commands)
            link_tools(bin_dir)
            if hub_json is not None:
                (home / ".persona").mkdir()
                (home / ".persona/hub.json").write_text(hub_json)
            result = subprocess.run(
                ["/bin/sh", str(ENTRYPOINT), "hub", "run", "one argument"],
                env={"PATH": str(bin_dir), "HOME": str(home), "PERSONA_ADMIN_ACCESS": access,
                     "PERSONA_ADMIN_CIDRS": cidrs, "PERSONA_PUBLIC_URL": public_url},
                capture_output=True, text=True, timeout=5,
            )
            self.assertEqual(result.returncode, exit_code, result.stderr)
            self.assertEqual(result.stdout.splitlines(), ["hub", "run", "one argument"])
            self.assertEqual(sorted(p.name for p in bin_dir.iterdir()), sorted([*commands, *TOOLS]))
            files = {str(p.relative_to(home)): p for p in home.rglob("*") if p.is_file()}
            self.assertEqual(list(files), [".persona/hub.json"])
            written = files[".persona/hub.json"]
            return result.stderr, written.read_text(), written.stat().st_mode & 0o777

    def test_manager_gets_no_hub_config_or_reminders(self):
        # A manager in the hub's image (docs/assistant/managers.md, "Containers"): no hub.json is seeded, none of the hub's reminders.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            home = root / "home"
            home.mkdir()
            write_stubs(bin_dir, {
                "id": '#!/bin/sh\ncase "$1" in -u) echo 1000 ;; -un) echo persona ;; *) exit 1 ;; esac\n',
                "getent": '#!/bin/sh\nexit 1\n',
                "personad": '#!/bin/sh\nprintf "%s\\n" "$@"\n',
            })
            link_tools(bin_dir)
            result = subprocess.run(
                ["/bin/sh", str(ENTRYPOINT), "manager", "run", "--dir", "/home/persona/.persona/managers/m-0a1b2c"],
                env={"PATH": str(bin_dir), "HOME": str(home)}, capture_output=True, text=True, timeout=5,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines(), ["manager", "run", "--dir", "/home/persona/.persona/managers/m-0a1b2c"])
            self.assertEqual([p for p in home.rglob("*") if p.is_file()], [])
            self.assertNotIn("PERSONA_PUBLIC_URL", result.stderr)
            self.assertNotIn("PERSONA_ADMIN_ACCESS", result.stderr)

    def run_command(self, *args):
        """The entrypoint with args (no hub.json yet): what it ran personad with, its log, and the files it wrote."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            home = root / "home"
            home.mkdir()
            write_stubs(bin_dir, {
                "id": '#!/bin/sh\ncase "$1" in -u) echo 1000 ;; -un) echo persona ;; *) exit 1 ;; esac\n',
                "getent": '#!/bin/sh\nexit 1\n',
                "personad": '#!/bin/sh\nprintf "%s\\n" "$@"\n',
            })
            link_tools(bin_dir)
            result = subprocess.run(["/bin/sh", str(ENTRYPOINT), *args], env={"PATH": str(bin_dir), "HOME": str(home)},
                                    capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
            files = sorted(str(p.relative_to(home)) for p in home.rglob("*") if p.is_file())
            return result.stdout.splitlines(), result.stderr, files

    def test_the_hub_runs_under_the_supervisor(self):
        # The image's command: personad supervise, which runs the built-in manager beside the hub in service mode and
        # becomes the hub itself otherwise (internal/supervise).
        ran, logs, files = self.run_command("hub", "run")
        self.assertEqual(ran, ["supervise"])
        self.assertEqual(files, [".persona/hub.json"])
        self.assertIn("PERSONA_ADMIN_ACCESS is not set", logs)
        # Any other command goes to personad as it is.
        self.assertEqual(self.run_command("hub", "pair")[0], ["hub", "pair"])

    def test_manager_only_container_is_not_a_hub(self):
        # The built-in manager in a container of its own (compose.yaml's commented example): no hub.json, no reminders.
        ran, logs, files = self.run_command("supervise", "--manager-only", "--hub", "http://hub:18660")
        self.assertEqual(ran, ["supervise", "--manager-only", "--hub", "http://hub:18660"])
        self.assertEqual(files, [])
        self.assertNotIn("PERSONA_PUBLIC_URL", logs)
        self.assertNotIn("PERSONA_ADMIN_ACCESS", logs)
        # The flag anywhere, in every spelling Go's flags take as true.
        for args in (("supervise", "--hub", "http://hub:18660", "--manager-only"), ("supervise", "--manager-only=true"),
                     ("supervise", "-manager-only"), ("supervise", "-manager-only=TRUE"), ("supervise", "--manager-only=1"),
                     ("supervise", "--manager-only=t"), ("supervise", "--manager-only=false", "--manager-only")):
            with self.subTest(args=args):
                ran, logs, files = self.run_command(*args)
                self.assertEqual((ran, files), (list(args), []))
                self.assertNotIn("PERSONA_ADMIN_ACCESS", logs)
        # False, or overridden by a later false: the hub's container (hub.json written, the hub's reminders).
        for args in (("supervise", "--manager-only=false"), ("supervise", "--manager-only", "-manager-only=0")):
            with self.subTest(args=args):
                ran, logs, files = self.run_command(*args)
                self.assertEqual((ran, files), (list(args), [".persona/hub.json"]))
                self.assertIn("PERSONA_ADMIN_ACCESS is not set", logs)

    def test_desktop_api_guidance_is_independent_of_admin_access(self):
        for access in ("", "lan", "loopback", "cidrs", "any"):
            with self.subTest(access=access):
                logs, _, _ = self.run_entrypoint(True, access, "192.0.2.1/32")
                for expected in ("gvisor", "tls_required", "security.trusted_cidrs", "netstat -tn",
                                 "docs/operations/docker.md", "127.0.0.1"):
                    self.assertIn(expected, logs)
                self.assertNotIn("every browser reaches the hub from the bridge's gateway", logs)

    def test_linux_does_not_get_desktop_guidance(self):
        self.assertNotIn("Docker Desktop", self.run_entrypoint(False)[0])

    def test_admin_reminders_are_preserved(self):
        self.assertIn("PERSONA_ADMIN_ACCESS is not set", self.run_entrypoint(False)[0])
        self.assertIn("without PERSONA_ADMIN_CIDRS", self.run_entrypoint(True, "cidrs")[0])
        self.assertIn("is not loopback, lan, cidrs or any", self.run_entrypoint(False, "invalid")[0])

    def test_hub_exit_status_is_preserved(self):
        self.run_entrypoint(True, "any", exit_code=23)

    def test_first_start_writes_container_defaults(self):
        logs, text, mode = self.run_entrypoint(False, "lan")
        self.assertEqual(json.loads(text), {
            "public_url": "",
            "public": {"tls_listen": ":18662"},
            "admin": {"first_setup": "code"},
            "machines": [],
            "assistant": {"mode": "service"},
        })
        # No token: the hub makes one when it first reads the file, as with db.sh's.
        self.assertNotIn("token", text)
        self.assertEqual(mode, 0o600)
        self.assertIn("first start: wrote hub.json", logs)
        self.assertIn("PERSONA_PUBLIC_URL is not set", logs)

    def test_first_start_with_public_url_does_not_nag(self):
        logs, text, _ = self.run_entrypoint(False, "lan", public_url="http://nas.example.com:18660")
        self.assertEqual(json.loads(text)["public_url"], "")
        self.assertNotIn("PERSONA_PUBLIC_URL is not set", logs)

    def test_existing_hub_json_is_never_touched(self):
        for existing in ('{"token": "t", "public_url": "http://nas.example.com:18660"}\n',
                         '{"token": "t", "public_url": "", "machines": [{"id": "laptop"}]}\n',
                         'not json at all'):
            with self.subTest(existing=existing):
                logs, text, _ = self.run_entrypoint(False, "lan", hub_json=existing)
                self.assertEqual(text, existing)
                self.assertNotIn("first start", logs)

    def test_root_path_defaults_puid_from_the_data_folder(self):
        text = ENTRYPOINT.read_text()
        # Set in .env: as before. Unset: the data folder's owner, never root (1000 then).
        self.assertIn('if [ -z "${PUID:-}" ] || [ -z "${PGID:-}" ]; then', text)
        self.assertIn('PUID="${PUID:-$owner}"', text)
        self.assertIn('if [ "$owner" = 0 ]; then owner=1000; fi', text)
        self.assertIn('if [ "$PUID" -eq 0 ]; then', text)


class PersonadWrapperTests(unittest.TestCase):
    """deploy/docker/personad.sh: the database password from .env, or the generated one from the data folder."""

    def run_wrapper(self, url, password_file=None, uid="1000", readable=True):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            binary = root / "personad-real"
            binary.write_text('#!/bin/sh\nprintf "%s\\n" "${PERSONA_DATABASE_URL:-<unset>}" "$@"\n')
            binary.chmod(0o755)
            write_stubs(bin_dir, {
                "id": '#!/bin/sh\necho ' + uid + '\n',
                "su-exec": '#!/bin/sh\n[ "$1" = persona ] || exit 9\nshift\nexec "$@"\n',
            })
            link_tools(bin_dir)
            wrapper = root / "personad.sh"
            wrapper.write_text(WRAPPER.read_text().replace("/opt/persona/personad", str(binary)))
            home = root / "home"
            (home / ".persona/docker").mkdir(parents=True)
            if password_file is not None:
                (home / ".persona/docker/db-password").write_text(password_file)
                if not readable:
                    (home / ".persona/docker/db-password").chmod(0)
            env = {"PATH": str(bin_dir), "HOME": str(home)}
            if url is not None:
                env["PERSONA_DATABASE_URL"] = url
            result = subprocess.run(["/bin/sh", str(wrapper), "hub", "pair"], env=env,
                                    capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
            lines = result.stdout.splitlines()
            self.assertEqual(lines[1:], ["hub", "pair"])
            return lines[0], result.stderr

    def test_env_password_wins(self):
        url = "postgres://persona:fromenv1@postgres:5432/persona?sslmode=disable"
        for uid in ("0", "1000"):
            with self.subTest(uid=uid):
                self.assertEqual(self.run_wrapper(url, "generated2", uid), (url, ""))

    def test_generated_password_fills_an_empty_one(self):
        for url in ("postgres://persona:@postgres:5432/persona?sslmode=disable",
                    "postgres://persona@postgres:5432/persona?sslmode=disable"):
            with self.subTest(url=url):
                got, logs = self.run_wrapper(url, "abc123\n", "0")
                self.assertEqual(got, "postgres://persona:abc123@postgres:5432/persona?sslmode=disable")
                self.assertEqual(logs, "")

    def test_missing_or_bad_password_file_warns(self):
        url = "postgres://persona:@postgres:5432/persona?sslmode=disable"
        got, logs = self.run_wrapper(url)
        self.assertEqual(got, url)
        self.assertIn("no database password", logs)
        got, logs = self.run_wrapper(url, "not ok!")
        self.assertEqual(got, url)
        self.assertIn("not a password of letters and digits", logs)
        if os.geteuid() != 0:  # root reads a 0000 file anyway
            got, logs = self.run_wrapper(url, "abc123", readable=False)
            self.assertEqual(got, url)
            self.assertIn("cannot be read by uid 1000", logs)

    def test_other_urls_are_left_alone(self):
        for url in (None, "", "postgres://postgres:5432/persona", "postgres://db/x@y"):
            with self.subTest(url=url):
                got, logs = self.run_wrapper(url, "abc123")
                self.assertEqual(got, url or "<unset>")
                self.assertEqual(logs, "")


class ComposeTests(unittest.TestCase):
    def postgres_script(self):
        lines = COMPOSE.read_text().splitlines()
        start = lines.index("      - |") + 1
        end = lines.index("      - postgres-entrypoint")
        return "\n".join(line[8:] for line in lines[start:end]).replace("$$", "$") + "\n"

    def run_postgres_wrapper(self, password, existing=None, database=False, args=None, server=None):
        """With args (the container's command): returns what reaches docker-entrypoint.sh. server: what the stub
        `postgres -V` and `pg_config --configure` print (None: no pg_config); None: neither is there."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            pgdata = root / "pgdata"
            if database:
                pgdata.mkdir()
                (pgdata / "PG_VERSION").write_text("18\n")
            secrets = root / "secrets"
            secrets.mkdir()
            if existing is not None:
                (secrets / "db-password").write_text(existing)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            write_stubs(bin_dir, {"docker-entrypoint.sh": '#!/bin/sh\nprintf "%s\\n" "$POSTGRES_PASSWORD" "$@"\n'})
            if server is not None:
                version, configure = server
                write_stubs(bin_dir, {"postgres": "#!/bin/sh\necho '" + version + "'\n"})
                if configure is not None:
                    write_stubs(bin_dir, {"pg_config": "#!/bin/sh\necho \"" + configure + "\"\n"})
            for name in ("tr", "head", "mv", "cat", "sed", "grep"):
                os.symlink(shutil.which(name), bin_dir / name)
            script = self.postgres_script().replace("/run/persona", str(secrets))
            result = subprocess.run(["/bin/sh", "-c", script, "postgres-entrypoint", *(args or ["postgres"])],
                                    env={"PATH": str(bin_dir), "POSTGRES_PASSWORD": password, "PGDATA": str(pgdata)},
                                    capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
            got, *passed = result.stdout.splitlines()
            if args:
                return passed
            self.assertEqual(passed, ["postgres"])
            stored = secrets / "db-password"
            return got, (stored.read_text() if stored.exists() else None), \
                (stored.stat().st_mode & 0o777 if stored.exists() else None), result.stderr

    def test_env_password_is_used_and_nothing_is_written(self):
        self.assertEqual(self.run_postgres_wrapper("fromenv1"), ("fromenv1", None, None, ""))

    def test_first_start_makes_up_a_password_once(self):
        got, stored, mode, logs = self.run_postgres_wrapper("")
        self.assertEqual(len(got), 48)
        self.assertTrue(all(c in "0123456789abcdef" for c in got))
        self.assertEqual(stored, got)
        self.assertEqual(mode, 0o600)
        self.assertIn("first start", logs)
        again, stored, _, logs = self.run_postgres_wrapper("", existing=got)
        self.assertEqual((again, stored, logs), (got, got, ""))

    def test_an_existing_database_gets_no_made_up_password(self):
        got, stored, _, logs = self.run_postgres_wrapper("", database=True)
        self.assertEqual((got, stored), ("", None))
        self.assertIn("the database already exists", logs)
        # The kept password still goes in (postgres ignores it once the database exists).
        got, stored, _, logs = self.run_postgres_wrapper("", existing="abc123", database=True)
        self.assertEqual((got, stored, logs), ("abc123", "abc123", ""))

    def test_wal_compression_auto_is_lz4_only_where_postgres_takes_it(self):
        # PostgreSQL 14 and older reject lz4, and so does a build without it: postgres would not start.
        lz4 = "'--prefix=/usr/local' '--with-lz4' '--with-zstd'"
        for name, version, configure, given, want in (
            ("18 with lz4", "18.6", lz4, "auto", "lz4"),
            ("14", "14.19", lz4, "auto", "off"),
            ("built without lz4", "16.10", "'--prefix=/usr/local' '--with-zstd'", "auto", "off"),
            ("no pg_config", "18.6", None, "auto", "off"),
            ("explicit value", "14.19", lz4, "pglz", "pglz"),
        ):
            with self.subTest(name):
                command = ["postgres", "-c", "synchronous_commit=on", "-c", "wal_compression=" + given, "-c", "x=has space"]
                passed = self.run_postgres_wrapper("pw", args=command, server=("postgres (PostgreSQL) " + version, configure))
                command[4] = "wal_compression=" + want
                self.assertEqual(passed, command)

    def test_compose_needs_only_the_image(self):
        text = COMPOSE.read_text()
        self.assertIn("image: ${PERSONA_IMAGE-${JARVIS_IMAGE:-}}", text)
        for name in ("PERSONA_DB_PASSWORD", "PERSONA_PUBLIC_URL", "PUID", "PGID"):
            self.assertNotIn("${" + name + ":?", text)
            self.assertNotIn("${" + name + ":-1000}", text)
        # The hub and postgres agree on where the generated password lives.
        self.assertIn("- ./data/docker:/run/persona", text)
        self.assertIn("PERSONA_DATABASE_URL: postgres://${PERSONA_DB_USER:-jarvis}:${PERSONA_DB_PASSWORD-${JARVIS_DB_PASSWORD:-}}@postgres:5432/", text)
        self.assertIn('$PERSONA_HOME/docker/db-password', WRAPPER.read_text())

    def test_compose_has_no_required_check_inside_a_default(self):
        # Compose before 5.0.2 evaluates `${A-${B:?…}}`'s default even when A is set: installs with only A would fail.
        nested, depth, text, i = [], 0, COMPOSE.read_text(), 0
        while i < len(text):
            if text.startswith("$$", i):
                i += 2
                continue
            if text.startswith("${", i):
                if depth and re.match(r"\$\{\w+:?\?", text[i:]):
                    nested.append(text[i:text.find("}", i) + 1])
                depth += 1
                i += 2
                continue
            if text[i] == "}" and depth:
                depth -= 1
            i += 1
        self.assertEqual(nested, [])

    DISK_WRITE_DEFAULTS = {
        "PERSONA_HUB_HEALTHCHECK_INTERVAL": "30s",
        "PERSONA_HUB_HEALTHCHECK_TIMEOUT": "5s",
        "PERSONA_HUB_HEALTHCHECK_RETRIES": "3",
        "PERSONA_LOG_DRIVER": "json-file",
        "PERSONA_LOG_MAX_SIZE": "10m",
        "PERSONA_LOG_MAX_FILE": "3",
        "PERSONA_POSTGRES_SYNCHRONOUS_COMMIT": "on",
        "PERSONA_POSTGRES_WAL_COMPRESSION": "auto",
        "PERSONA_POSTGRES_CHECKPOINT_TIMEOUT": "15min",
        "PERSONA_POSTGRES_WAL_WRITER_DELAY": "200ms",
        "PERSONA_POSTGRES_HEALTHCHECK_INTERVAL": "5s",
        "PERSONA_POSTGRES_HEALTHCHECK_TIMEOUT": "5s",
        "PERSONA_POSTGRES_HEALTHCHECK_RETRIES": "30",
    }

    def test_compose_disk_write_defaults_match_the_env_example_and_docs(self):
        text = COMPOSE.read_text()
        names = "|".join(self.DISK_WRITE_DEFAULTS)
        self.assertEqual(dict(re.findall(r"\$\{(" + names + r"):-([^}]*)\}", text)), self.DISK_WRITE_DEFAULTS)
        # The image's entry point runs its first-start setup only when the command starts with postgres.
        self.assertIn("    command:\n      - postgres\n      - -c\n      - synchronous_commit=", text)
        self.assertIn("\n    logging: &logging\n", text)
        self.assertIn("\n    logging: *logging\n", text)
        env_example = (DOCKER / ".env.example").read_text()
        doc = (ROOT / "docs/operations/docker.md").read_text()
        for name, default in self.DISK_WRITE_DEFAULTS.items():
            self.assertIn("# " + name + "=" + default + "\n", env_example)
            self.assertIn("`" + name + "`", doc)

    def test_compose_config_resolves_the_disk_write_settings(self):
        if not shutil.which("docker"):
            self.skipTest("Docker Compose is unavailable; no containers are started by this check")
        overrides = {"PERSONA_POSTGRES_SYNCHRONOUS_COMMIT": "off", "PERSONA_POSTGRES_HEALTHCHECK_INTERVAL": "60s",
                     "PERSONA_POSTGRES_HEALTHCHECK_RETRIES": "5", "PERSONA_LOG_DRIVER": "local"}
        for env_values in ({}, overrides):
            with self.subTest(overrides=bool(env_values)), tempfile.TemporaryDirectory() as directory:
                env = {"PATH": os.environ["PATH"], "HOME": directory, "PERSONA_IMAGE": "example.invalid/fixture:1",
                       "COMPOSE_PROJECT_NAME": "persona-test-" + os.urandom(4).hex(), **env_values}
                if os.environ.get("PERSONA_TEST_DOCKER_CONFIG"):
                    env["DOCKER_CONFIG"] = os.environ["PERSONA_TEST_DOCKER_CONFIG"]
                result = subprocess.run(["docker", "compose", "-f", str(COMPOSE), "config", "--format", "json"],
                                        env=env, capture_output=True, text=True, timeout=15)
                if "is not a docker command" in result.stderr:
                    self.skipTest("Docker Compose is unavailable")
                self.assertEqual(result.returncode, 0, result.stderr)
                services = json.loads(result.stdout)["services"]
                values = {**self.DISK_WRITE_DEFAULTS, **env_values}
                postgres = services["postgres"]
                self.assertEqual(postgres["command"], [
                    "postgres",
                    "-c", "synchronous_commit=" + values["PERSONA_POSTGRES_SYNCHRONOUS_COMMIT"],
                    "-c", "wal_compression=" + values["PERSONA_POSTGRES_WAL_COMPRESSION"],
                    "-c", "checkpoint_timeout=" + values["PERSONA_POSTGRES_CHECKPOINT_TIMEOUT"],
                    "-c", "wal_writer_delay=" + values["PERSONA_POSTGRES_WAL_WRITER_DELAY"],
                ])
                self.assertEqual(postgres["healthcheck"]["retries"], int(values["PERSONA_POSTGRES_HEALTHCHECK_RETRIES"]))
                self.assertEqual(services["hub"]["healthcheck"]["retries"], 3)
                self.assertNotIn("test", services["hub"]["healthcheck"])
                for service in ("hub", "postgres"):
                    self.assertEqual(services[service]["logging"], {
                        "driver": values["PERSONA_LOG_DRIVER"],
                        "options": {"max-size": "10m", "max-file": "3"},
                    })

    def test_tls_port_is_published_and_exposed(self):
        self.assertIn('- "${PERSONA_TLS_PORT-${JARVIS_TLS_PORT:-18662}}:18662"', COMPOSE.read_text())
        self.assertIn('"tls_listen": ":%s"', ENTRYPOINT.read_text())
        self.assertIn("TLS_PORT=18662", ENTRYPOINT.read_text())
        self.assertIn("EXPOSE 18660 18662", (ROOT / "Dockerfile").read_text())

    def test_web_shared_fixture_imports_are_in_the_image(self):
        dockerfile = (ROOT / "Dockerfile").read_text()
        context = (ROOT / ".dockerignore").read_text().splitlines()
        imports = re.compile(r'''["']((?:\.\./)+spec/fixtures/[^"']+\.json)["']''')
        groups = set()
        for source in (ROOT / "web/src").rglob("*"):
            if source.suffix not in (".ts", ".tsx"):
                continue
            for relative in imports.findall(source.read_text()):
                fixture = (source.parent / relative).resolve()
                self.assertTrue(list(fixture.parent.glob(fixture.name)), str(fixture.relative_to(ROOT)))
                groups.add(fixture.relative_to(ROOT / "spec/fixtures").parts[0])
        self.assertTrue(groups)
        # The whole folder goes in, so a new case folder needs no Docker change (#5197).
        self.assertIn("!spec/fixtures/", context)
        self.assertIn("COPY spec/fixtures/ /src/spec/fixtures/", dockerfile)
        self.assertFalse([line for line in context if line.startswith("!spec/fixtures/") and line != "!spec/fixtures/"])

if __name__ == "__main__":
    unittest.main()
