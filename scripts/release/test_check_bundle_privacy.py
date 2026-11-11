from contextlib import redirect_stdout
import gzip
import hashlib
import io
import ipaddress
from pathlib import Path
import plistlib
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch


sys.path.insert(0, str(Path(__file__).resolve().parent))
import check_bundle_privacy as cbp


DEPENDENCIES = {"golang.org/x/crypto", "tailscale.com", "github.com/tailscale/web-client-prebuilt"}
NETWORKS = tuple(ipaddress.ip_network((address, prefix)) for address, prefix in (
    (0x0A000000, 8), (0xAC100000, 12), (0xC0A80000, 16),
))
METADATA = str(ipaddress.ip_address(0xA9FEA9FE))
CONTACT = '<a href="mailto:support+webclient@' + 'tailscale.com" class="link">contact us</a>'
SSH_REQUEST = "ping@" + "openssh.com"
EMAIL = "person@" + "example.dev"
SSH_NAMES = (
    "aes128-gcm@" + "openssh.com",
    "aes256-gcm@" + "openssh.com",
    "curve25519-sha256@" + "libssh.org",
    "ecdsa-sha2-nistp256-cert-v01@" + "openssh.com",
    "ecdsa-sha2-nistp384-cert-v01@" + "openssh.com",
    "ecdsa-sha2-nistp521-cert-v01@" + "openssh.com",
    "ssh-ed25519-cert-v01@" + "openssh.com",
)
GO_SSH_NAMES = SSH_NAMES + tuple(name + "@" + "openssh.com" for name in (
    "chacha20-poly1305", "hmac-sha2-256-etm", "hmac-sha2-512-etm",
    "rsa-sha2-256-cert-v01", "rsa-sha2-512-cert-v01", "ssh-rsa-cert-v01", "ssh-dss-cert-v01",
    "sk-ssh-ed25519", "sk-ssh-ed25519-cert-v01", "sk-ecdsa-sha2-nistp256", "sk-ecdsa-sha2-nistp256-cert-v01",
    "kex-strict-c-v00", "kex-strict-s-v00", "ping", "auth-agent-req", "auth-agent",
    "direct-streamlocal", "forwarded-streamlocal", "streamlocal-forward", "cancel-streamlocal-forward", "keepalive",
    "statvfs", "fstatvfs", "fsync", "hardlink", "posix-rename",
))


class SSHFieldTests(unittest.TestCase):
    def test_complete_known_names_and_name_lists_are_classified(self):
        for text in (*SSH_NAMES, ",".join(SSH_NAMES), ",".join(reversed(SSH_NAMES))):
            for public in (False, True):
                with self.subTest(length=len(text), public=public):
                    self.assertEqual(cbp.text_kinds(text, (), whole_short=public), set())

    def test_url_and_other_contexts_are_not_protocol_fields(self):
        for name in SSH_NAMES:
            samples = ("mailto:" + name, "https://" + name, "prefix" + name, name + "-suffix.com",
                       " " + name, name + " ", '"' + name + '"', "<string>" + name + "</string>",
                       name + ".cn", name.upper(), name.replace("@", "+account@"))
            for text in samples:
                with self.subTest(name_length=len(name), field_length=len(text)):
                    self.assertEqual(cbp.generic_text(text, frozenset()), text)
                    self.assertIn("email", cbp.text_kinds(text, ()))
        for domain in ("openssh.com", "libssh.org"):
            self.assertIn("email", cbp.text_kinds("person@" + domain, ()))

    def test_partial_and_unknown_name_lists_are_not_classified(self):
        name = SSH_NAMES[0]
        for text in ("," + name, name + ",", name + ",," + name, name + ",unknown",
                     "unknown," + name, name + "," + EMAIL, name + ", " + name,
                     "mailto:" + name + "," + name):
            with self.subTest(field_length=len(text)):
                self.assertEqual(cbp.generic_text(text, frozenset()), text)
                self.assertIn("email", cbp.text_kinds(text, ()))

    def test_registered_values_win_for_names_and_lists(self):
        for name in SSH_NAMES:
            for word in (name, *name.split("@")):
                for text in (name, SSH_NAMES[0] + "," + name):
                    for public in (False, True):
                        with self.subTest(field_length=len(text), word_length=len(word), public=public):
                            self.assertIn(f"private value ({len(word)} chars)",
                                          cbp.text_kinds(text, (word,), whole_short=public))

    def test_unknown_tld_suffix_is_not_removed_by_protocol_classification(self):
        for name in SSH_NAMES:
            text = name + "suffix"
            self.assertEqual(cbp.generic_text(text, frozenset()), text)
            # Preserve the generic detector's existing TLD policy, while registered values still fail.
            self.assertIn(f"private value ({len(name)} chars)", cbp.text_kinds(text, (name,)))


class BinaryNoiseEmailTests(unittest.TestCase):
    """Machine code that reads like person@example.com is not an address (#4497); addresses people write still are."""

    def test_noise_is_not_an_email(self):
        for local, domain in (("B", "9.Sg"), ("xQ", "7.De"), ("a", "12.34.cN")):
            with self.subTest(domain=domain):
                self.assertEqual(cbp.text_kinds(local + "@" + domain, ()), set())

    def test_real_looking_addresses_still_count(self):
        for local, domain in (("person", "company.com"), ("Person", "Company.com"), ("OPS", "COMPANY.COM"),
                              ("me", "team9.io")):
            with self.subTest(domain=domain):
                self.assertIn("email", cbp.text_kinds(local + "@" + domain, ()))


class PublicConstantTests(unittest.TestCase):
    def kinds(self, text, words=(), dependencies=DEPENDENCIES):
        return cbp.text_kinds(text, words, whole_short=True, dependencies=dependencies)

    def test_openssh_request_is_classified(self):
        self.assertEqual(self.kinds("failed to unmarshal " + SSH_REQUEST + " message: %w"), set())
        for text in ("person@" + "openssh.com", "prefix" + SSH_REQUEST, SSH_REQUEST + ".cn"):
            with self.subTest(text=text):
                self.assertIn("email", self.kinds(text))

    def test_ssh_wire_identifiers_in_fields_lists_and_format_strings(self):
        for name in GO_SSH_NAMES:
            for text in (name, '"' + name + '"', "failed to unmarshal " + name + " message: %w",
                         "algorithms=" + name + ",ssh-ed25519", "extension:" + name + ";next"):
                with self.subTest(text=text):
                    self.assertEqual(self.kinds(text, dependencies={"golang.org/x/crypto"}), set())
        self.assertEqual(self.kinds(",".join(GO_SSH_NAMES)), set())

    def test_ssh_classification_requires_crypto_except_existing_swift_fields(self):
        for dependencies in (frozenset(), DEPENDENCIES - {"golang.org/x/crypto"}):
            for name in GO_SSH_NAMES:
                with self.subTest(name=name, dependencies=dependencies):
                    self.assertIn("email", self.kinds("algorithm: " + name, dependencies=dependencies))
                    # Preserve the existing swift-nio-ssh complete-field exception.
                    if name not in SSH_NAMES:
                        self.assertIn("email", self.kinds(name, dependencies=dependencies))

    def test_ssh_domains_and_extended_identifiers_still_fail(self):
        for text in ("person@" + "openssh.com", "other@" + "libssh.org",
                     "aes128-gcm@" + "libssh.org", "curve25519-sha256@" + "openssh.com"):
            with self.subTest(text=text):
                self.assertIn("email", self.kinds(text))
                self.assertIn("email", self.kinds(",".join((*GO_SSH_NAMES, text))))
        for name in GO_SSH_NAMES:
            for boundary in "Az09._%+-":
                for text in (boundary + name, name + boundary):
                    with self.subTest(text=text):
                        self.assertEqual(cbp.generic_text(text, DEPENDENCIES), text)
            for text in ("prefix" + name, name + ".cn", name.replace("@", "+account@"), name.upper()):
                with self.subTest(text=text):
                    self.assertIn("email", self.kinds(text))

    def test_registered_ssh_values_win_over_public_classification(self):
        for name in GO_SSH_NAMES:
            for word in (name, *name.split("@")):
                with self.subTest(name=name, word=word):
                    self.assertIn(f"private value ({len(word)} chars)",
                                  self.kinds("algorithm: " + name, words=(word,)))

    def test_exact_upstream_contact_link_not_the_entire_domain(self):
        self.assertEqual(self.kinds(CONTACT), set())
        for text in ("person@" + "tailscale.com", "support+webclient@" + "tailscale.com",
                     CONTACT.replace("tailscale.com", "tailscale.com.cn"),
                     CONTACT.replace("contact us", "another account")):
            with self.subTest(text=text):
                self.assertIn("email", self.kinds(text))

    def test_full_rfc1918_networks_are_not_host_addresses(self):
        for network in NETWORKS:
            with self.subTest(network=str(network)):
                self.assertEqual(self.kinds("constant" + str(network) + "nextConstant"), set())
                self.assertIn("private IP", self.kinds(str(network.network_address)))
                self.assertIn("private IP", self.kinds(str(network.network_address) + "/24"))
                self.assertIn("private IP", self.kinds(str(network.network_address) + "/" + str(network.prefixlen) + "0"))
                self.assertIn("private IP", self.kinds(str(network.network_address + 17) + "/24"))

    def test_adjacent_cidrs_in_go_rodata(self):
        link_local = str(ipaddress.ip_network((0xA9FE0000, 16)))
        self.assertEqual(self.kinds(str(NETWORKS[2]) + link_local + "profiles_count"), set())
        self.assertIn("private IP", self.kinds(str(NETWORKS[2]) + "999nextConstant"))

    def test_cloud_metadata_requires_a_known_context(self):
        for text in ("constanthttp://" + METADATA + "/latest/api/token", "http://" + METADATA,
                     METADATA + str(ipaddress.ip_address(int(ipaddress.ip_address(METADATA)) - 1)) + "Metadata-Flavor"):
            with self.subTest(text=text):
                self.assertEqual(self.kinds(text), set())
        for text in (METADATA, "hub=" + METADATA, "https://" + METADATA,
                     "http://" + str(ipaddress.ip_address(int(ipaddress.ip_address(METADATA)) + 1))):
            with self.subTest(text=text):
                self.assertIn("private IP", self.kinds(text))

    def test_classification_requires_the_corresponding_dependency(self):
        samples = [(SSH_REQUEST, "golang.org/x/crypto", "email"),
                   (CONTACT, "github.com/tailscale/web-client-prebuilt", "email"),
                   (str(NETWORKS[0]), "tailscale.com", "private IP"),
                   ("http://" + METADATA, "tailscale.com", "private IP")]
        for text, dependency, kind in samples:
            with self.subTest(dependency=dependency, kind=kind):
                self.assertIn(kind, self.kinds(text, dependencies=()))
                self.assertIn(kind, self.kinds(text, dependencies=DEPENDENCIES - {dependency}))
                self.assertIn(kind, cbp.text_kinds(text, ()))

    def test_registered_values_are_never_removed(self):
        for text, word in ((SSH_REQUEST, SSH_REQUEST),
                           (CONTACT, "support+webclient@" + "tailscale.com"),
                           (str(NETWORKS[0]), str(NETWORKS[0].network_address)),
                           ("http://" + METADATA, METADATA)):
            with self.subTest(text=text):
                self.assertIn(f"private value ({len(word)} chars)", self.kinds(text, words=(word,)))

    def test_unrelated_private_data_next_to_public_constants_still_fails(self):
        address = str(NETWORKS[0].network_address + 17)
        text = f"{SSH_REQUEST} {CONTACT} {NETWORKS[0]} http://{METADATA} {EMAIL} {address} secret-placeholder"
        self.assertEqual(self.kinds(text, words=("secret-placeholder",)),
                         {"email", "private IP", "private value (18 chars)"})

    def test_short_word_matching_remains_unchanged(self):
        self.assertEqual(self.kinds("foobar", words=("foo",)), set())
        self.assertEqual(self.kinds("FOO foo", words=("foo",)), {"private value (3 chars)"})
        self.assertEqual(cbp.text_kinds("foobar", ("foo",)), {"private value (3 chars)"})


class PinnedPublicJSTests(unittest.TestCase):
    filename = "mermaid.min.js"
    fixture = b'const label = "remarkable";\n'

    def scan(self, data, words=("mark",), filename=None, pin=None, extra=None, hermes=None):
        with tempfile.TemporaryDirectory(prefix="bundle-privacy-js-") as directory:
            (Path(directory) / (filename or self.filename)).write_bytes(data)
            if extra is not None:
                (Path(directory) / "private.js").write_bytes(extra)
            if hermes is not None:
                (Path(directory) / "Persona").write_bytes(hermes)
            output = io.StringIO()
            pins = {self.filename: pin or hashlib.sha256(self.fixture).hexdigest()}
            with patch.object(sys, "argv", ["check_bundle_privacy.py", directory]), \
                    patch.object(cbp.pc, "module_paths", return_value=None), \
                    patch.object(cbp.pc, "terms", return_value=(words, set())), \
                    patch.object(cbp.persona_config, "load", return_value={}), \
                    patch.object(cbp, "PUBLIC_JS_SHA256", pins), redirect_stdout(output):
                try:
                    cbp.main()
                except SystemExit as error:
                    return error.code, output.getvalue()
            return 0, output.getvalue()

    def test_only_the_exact_resource_name_and_bytes_are_verified(self):
        resource = Path(cbp.pc.ROOT) / "apple/Persona/Resources/Render" / self.filename
        data = resource.read_bytes()
        self.assertTrue(cbp.verified_public_js(resource, data))
        self.assertFalse(cbp.verified_public_js("another.js", data))
        self.assertFalse(cbp.verified_public_js(resource, data + b" "))
        self.assertFalse(cbp.verified_public_js(resource, b"x" + data[1:]))
        self.assertFalse(cbp.verified_public_js(resource, data[:-1]))
        self.assertFalse(cbp.verified_public_js(resource, b""))

    def test_pinned_short_fragment_is_not_a_complete_private_word(self):
        code, output = self.scan(self.fixture)
        self.assertEqual(code, 0, output)
        code, output = self.scan(self.fixture, pin="0" * 64)
        self.assertNotEqual(code, 0)
        self.assertIn("private value", output)
        self.assertNotIn('"mark"', output)

    def test_changed_appended_and_truncated_resources_fall_back_to_strict_matching(self):
        for data in (self.fixture + b" ", b" " + self.fixture, self.fixture[:-1],
                     self.fixture + b'const account = "mark";'):
            with self.subTest(bytes=len(data)):
                code, output = self.scan(data)
                self.assertNotEqual(code, 0)
                self.assertIn("private value", output)
        code, output = self.scan(self.fixture, filename="another.js")
        self.assertNotEqual(code, 0)
        self.assertIn("private value", output)

    def test_complete_short_words_and_nonletter_values_still_fail(self):
        for text, word in (("'mark'", "mark"), ("'MARK'", "MARK"), ("'mark'", "MARK"),
                           ("remarkable", "remark"),
                           ("prefix1234suffix", "1234"), ("prefixa1b2suffix", "a1b2"),
                           ("prefixab-csuffix", "ab-c")):
            with self.subTest(length=len(word)):
                data = text.encode()
                code, output = self.scan(data, words=(word,), pin=hashlib.sha256(data).hexdigest())
                self.assertNotEqual(code, 0)
                self.assertIn("private value", output)
                self.assertNotIn(text, output)

    def test_generic_emails_and_private_ips_are_not_classified(self):
        address = str(NETWORKS[0].network_address + 17)
        data = (self.fixture.decode() + EMAIL + "\n" + address).encode()
        code, output = self.scan(data, pin=hashlib.sha256(data).hexdigest())
        self.assertNotEqual(code, 0)
        self.assertIn("email", output)
        self.assertIn("private IP", output)
        self.assertNotIn(EMAIL, output)
        self.assertNotIn(address, output)

    def test_hermes_tokens_share_the_pinned_resource_boundary(self):
        # A longer JavaScript token contains the short fragment, but a complete short word still fails.
        code, output = self.scan(self.fixture, hermes=b'["remarkable", (e) => { call(e); }]')
        self.assertEqual(code, 0, output)
        code, output = self.scan(self.fixture, hermes=b'["mark", "remarkable"]')
        self.assertNotEqual(code, 0)
        self.assertIn("Persona: private value (4 chars)", output)

    def test_hermes_boundary_requires_the_unmodified_resource(self):
        for data in (self.fixture + b" ", self.fixture[:-1], b"unrelated"):
            with self.subTest(bytes=len(data)):
                code, output = self.scan(data, hermes=b'["remarkable"]')
                self.assertNotEqual(code, 0)
                self.assertIn("Persona: private value (4 chars)", output)
        code, output = self.scan(self.fixture, pin="0" * 64, hermes=b'["remarkable"]')
        self.assertNotEqual(code, 0)
        self.assertIn("Persona: private value (4 chars)", output)

    def test_other_files_remain_strict_even_next_to_a_pinned_resource(self):
        code, output = self.scan(self.fixture, extra=self.fixture)
        self.assertNotEqual(code, 0)
        self.assertIn("private.js: private value", output)
        self.assertNotIn(self.filename + ": private value", output)

    def test_normalized_fragments_cannot_use_the_public_resource_boundary(self):
        text = "remarkable"
        self.assertEqual(cbp.text_kinds(text, ("mark",), public_js=True, original_text=text), set())
        self.assertIn("private value (4 chars)",
                      cbp.text_kinds(text, ("mark",), public_js=True, original_text="other" + text))
        self.assertIn("private value (4 chars)", cbp.text_kinds(text, ("mark",)))

    def test_provenance_and_scanning_share_the_same_byte_snapshot(self):
        with tempfile.TemporaryDirectory(prefix="bundle-privacy-snapshot-") as directory:
            path = Path(directory) / self.filename
            path.write_bytes(self.fixture)
            snapshot = path.read_bytes()
            path.write_bytes(b"different content")
            fields = list(cbp.texts(str(path), snapshot))
            self.assertEqual(fields, [self.fixture.decode().rstrip("\n")])
            self.assertNotEqual(fields, list(cbp.texts(str(path))))

    def test_actual_mermaid_resource_regression_and_complete_word_detection(self):
        resource = Path(cbp.pc.ROOT) / "apple/Persona/Resources/Render" / self.filename
        data = resource.read_bytes()
        code, output = self.scan(data, words=("ouch",), pin=cbp.PUBLIC_JS_SHA256[self.filename])
        self.assertEqual(code, 0, output)
        code, output = self.scan(data, words=("ouch",), pin="0" * 64)
        self.assertNotEqual(code, 0)
        code, output = self.scan(data, words=("body",), pin=cbp.PUBLIC_JS_SHA256[self.filename])
        self.assertNotEqual(code, 0)
        self.assertIn("private value", output)


class GoDependenciesTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="bundle-privacy-test-")
        self.addCleanup(self.temp.cleanup)
        self.sums = Path(self.temp.name) / "go.sum"
        self.sums.write_text("golang.org/x/crypto v1.0.0 h1:placeholder\n")
        self.tags = Path(self.temp.name) / "build-tags.txt"
        self.tags.write_text("public,ts_omit_aws,ts_omit_ssh\n")
        self.build_info = ("\tdep\tgolang.org/x/crypto\tv1.0.0\th1:placeholder\n"
                           "\tbuild\t-tags=public,ts_omit_aws,ts_omit_ssh\n")

    def dependencies(self, text, returncode=0):
        result = subprocess.CompletedProcess([], returncode, stdout=text)
        with patch.object(cbp.subprocess, "run", return_value=result):
            return cbp.go_dependencies("binary", str(self.sums), str(self.tags))

    def test_checksum_and_public_build_are_required(self):
        self.assertEqual(self.dependencies(self.build_info), {"golang.org/x/crypto"})
        self.assertEqual(self.dependencies(self.build_info.replace("h1:placeholder", "h1:other")), set())
        self.assertEqual(self.dependencies(self.build_info.replace("v1.0.0", "v2.0.0")), set())

    def test_only_the_exact_published_tags_mark_a_public_build(self):
        for tags in ("public", "debug", "public,ts_omit_aws", "public,ts_omit_aws,ts_omit_ssh,debug",
                     "public,ts_omit_aws,ts_omit_ssh,ts_omit_ssh", "ts_omit_aws,ts_omit_ssh"):
            with self.subTest(tags=tags):
                info = self.build_info.replace("-tags=public,ts_omit_aws,ts_omit_ssh", "-tags=" + tags)
                self.assertEqual(self.dependencies(info), set())
        # The file itself may hold only the marker and ts_omit_ feature tags.
        for line in ("public,ts_omit_aws,ts_omit_ssh,debug", "ts_omit_aws,ts_omit_ssh", "public,public"):
            with self.subTest(file=line):
                self.tags.write_text(line + "\n")
                self.assertEqual(self.dependencies(self.build_info.replace(
                    "-tags=public,ts_omit_aws,ts_omit_ssh", "-tags=" + line)), set())
        self.tags.unlink()
        self.assertEqual(self.dependencies(self.build_info), set())

    def test_the_checked_in_tags_file_is_a_public_tag_set(self):
        self.assertIsNotNone(cbp.public_build_tags())

    def test_module_replacements_are_not_trusted(self):
        replaced = self.build_info.replace("\n\tbuild", "\n\t=>\t./local-module\t(devel)\n\tbuild")
        self.assertEqual(self.dependencies(replaced), set())
        no_sum = self.build_info.replace("\th1:placeholder", "")
        self.assertEqual(self.dependencies(no_sum), set())

    def test_unrelated_replacements_do_not_remove_verified_dependencies(self):
        info = self.build_info + "\tdep\tother.module\tv1.0.0\n\t=>\t./local-module\t(devel)\n"
        self.assertEqual(self.dependencies(info), {"golang.org/x/crypto"})

    def test_tool_failures_and_missing_sums_fail_closed(self):
        self.assertEqual(self.dependencies(self.build_info, returncode=1), set())
        for error in (FileNotFoundError(), subprocess.TimeoutExpired("go", 10)):
            with self.subTest(error=type(error).__name__):
                with patch.object(cbp.subprocess, "run", side_effect=error):
                    self.assertEqual(cbp.go_dependencies("binary", str(self.sums), str(self.tags)), set())
        self.sums.unlink()
        self.assertEqual(cbp.go_dependencies("binary", str(self.sums), str(self.tags)), set())


class BundleScanIntegrationTests(unittest.TestCase):
    def scan(self, text, words=(), public=True, config=None, dependencies=DEPENDENCIES):
        with tempfile.TemporaryDirectory(prefix="bundle-privacy-test-") as directory:
            (Path(directory) / "binary").write_bytes(text if isinstance(text, bytes) else text.encode())
            output = io.StringIO()
            args = ["check_bundle_privacy.py"] + (["--whole-short"] if public else []) + [directory]
            with patch.object(sys, "argv", args), patch.object(cbp.pc, "module_paths", return_value=None), \
                    patch.object(cbp.pc, "terms", return_value=(words, set())), \
                    patch.object(cbp.persona_config, "load", return_value=config or {}), \
                    patch.object(cbp, "go_dependencies", return_value=dependencies), redirect_stdout(output):
                try:
                    cbp.main()
                except SystemExit as error:
                    return error.code, output.getvalue()
            return 0, output.getvalue()

    def test_public_binary_scan_classifies_verified_constants(self):
        text = f"{SSH_REQUEST}\n{CONTACT}\n{NETWORKS[0]}\nhttp://{METADATA}"
        code, output = self.scan(text)
        self.assertEqual(code, 0, output)
        self.assertIn("check_bundle_privacy: clean", output)

    def test_public_binary_ssh_rodata_regression(self):
        text = "\0".join((",".join(GO_SSH_NAMES), *("ssh: %s [" + name + "]" for name in GO_SSH_NAMES)))
        code, output = self.scan(text)
        self.assertEqual(code, 0, output)
        self.assertIn("check_bundle_privacy: clean", output)
        for dependencies in (frozenset(), DEPENDENCIES - {"golang.org/x/crypto"}):
            code, output = self.scan(text, dependencies=dependencies)
            self.assertNotEqual(code, 0)
            self.assertIn("email", output)
        for address in ("person@" + "openssh.com", "other@" + "libssh.org"):
            code, output = self.scan(text + "," + address)
            self.assertNotEqual(code, 0)
            self.assertIn("email", output)
            self.assertNotIn(address, output)
        for name in GO_SSH_NAMES:
            code, output = self.scan(text, words=(name,))
            self.assertNotEqual(code, 0)
            self.assertIn("private value", output)
            self.assertNotIn(name, output)

    def test_registered_values_still_fail_and_are_not_logged(self):
        for text, value in ((CONTACT, "support+webclient@" + "tailscale.com"),
                            ("http://" + METADATA, METADATA)):
            with self.subTest(value=value):
                code, output = self.scan(text, words=(value,))
                self.assertNotEqual(code, 0)
                self.assertIn("private value", output)
                self.assertNotIn(value, output)

    def test_unknown_private_data_still_fails_and_is_not_logged(self):
        address = str(NETWORKS[1].network_address + 17)
        code, output = self.scan(CONTACT + "\n" + EMAIL + "\n" + address)
        self.assertNotEqual(code, 0)
        self.assertIn("email", output)
        self.assertIn("private IP", output)
        self.assertNotIn(EMAIL, output)
        self.assertNotIn(address, output)

    def test_gzip_members_in_a_binary_are_scanned_inflated(self):
        # A Go build embeds the runtime packages as gzipped tars (server/internal/runtimepkg): their text must count.
        def member(text):
            archive = io.BytesIO()
            with tarfile.open(fileobj=archive, mode="w") as tar:
                data = text.encode()
                info = tarfile.TarInfo("runtime-sdk/lib/runtime.js")
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
            return gzip.compress(archive.getvalue(), compresslevel=9, mtime=0)

        word = "privatehost"
        plain = member("const a = 1;\n" * 50)
        # Filler that does not compress to nothing, so half the member still holds the start of the file.
        long = member("// " + word + " " + "".join("%x" % i for i in range(3000)))
        for name, data in (
                ("alone", member("export const host = '" + word + "'")),
                ("inside a binary", b"\x7fELF\x00" * 64 + member("// " + word) + b"\x00\x1f\x8b\x08junk" * 8),
                ("after another member", plain + member("// " + word)),
                ("cut short", long[:len(long) // 2])):
            with self.subTest(name):
                self.assertNotIn(word.encode(), data)
                code, output = self.scan(data, words=(word,))
                self.assertNotEqual(code, 0)
                self.assertIn("private value", output)
                self.assertNotIn(word, output)
        code, output = self.scan(member("mail " + EMAIL))
        self.assertNotEqual(code, 0)
        self.assertIn("email", output)
        code, output = self.scan(plain + b"\x1f\x8b\x08\x00" * 4, words=(word,))
        self.assertEqual(code, 0, output)

    def test_app_accepts_only_the_exact_configured_public_relay_url(self):
        relay = "https://relay.example.test/relay"
        config = {"app": {"push_relay": relay}}
        words = ("relay.example.test", relay)
        for text in (relay, '<string>' + relay + '</string>'):
            with self.subTest(text=text):
                code, output = self.scan(text, words=words, public=False, config=config)
                self.assertEqual(code, 0, output)
        for text in (relay + "/private", relay + "?token=placeholder", relay + "#context",
                     relay + ".other", "relay.example.test", "person@relay.example.test",
                     "https://user@relay.example.test/relay", relay + "\nrelay.example.test"):
            with self.subTest(text=text):
                code, output = self.scan(text, words=words, public=False, config=config)
                self.assertNotEqual(code, 0)
                self.assertIn("private value", output)
        code, _ = self.scan(relay, words=words, public=True, config=config)
        self.assertNotEqual(code, 0)

    def test_non_public_or_invalid_relay_urls_remain_private(self):
        for relay in ("http://relay.example.test/relay", "https://user:password@relay.example.test/relay",
                      "https://relay.example.test/relay?token=placeholder", "https://relay.example.test/other",
                      "https://localhost/relay", "https://203.0.113.17/relay", "https://[invalid/relay",
                      "https://relay.example.test:8443/relay"):
            with self.subTest(relay=relay):
                code, output = self.scan(relay, words=(relay,), public=False, config={"app": {"push_relay": relay}})
                self.assertNotEqual(code, 0)
                self.assertIn("private value", output)
        for relay in (None, [], {}):
            self.assertEqual(cbp.remove_public_relay_url("private-placeholder", relay), "private-placeholder")

    def test_published_binary_accepts_only_the_exact_public_download_url(self):
        site = "https://site.example.test"
        url = site + "/dl"
        words = ("site.example.test", site)
        for config in ({"publish": {"site_url": site}}, {"publish": {"site_url": site + "/"}}):
            code, output = self.scan(url, words=words, config=config)
            self.assertEqual(code, 0, output)
        config = {"publish": {"site_url": site}}
        for text in (site, url + "/private", url + "?token=placeholder", url + ".other", "site.example.test",
                     "person@site.example.test", "https://user@site.example.test/dl", url + "\nsite.example.test"):
            with self.subTest(text=text):
                code, output = self.scan(text, words=words, config=config)
                self.assertNotEqual(code, 0)
                self.assertIn("private value", output)
                self.assertNotIn("site.example.test", output)
        code, _ = self.scan(url, words=words, public=False, config=config)  # app bundles do not carry it
        self.assertNotEqual(code, 0)
        for bad in ("http://site.example.test", "https://203.0.113.17", "https://site.example.test:8443"):
            with self.subTest(site=bad):
                code, _ = self.scan(bad + "/dl", words=(bad + "/dl",), config={"publish": {"site_url": bad}})
                self.assertNotEqual(code, 0)

    def test_published_binary_accepts_only_the_exact_configured_update_url(self):
        # publish.update_url (#4522): the binaries carry it instead of <site>/dl, exactly as configured.
        url = "https://releases.example.test/owner/repo/releases/latest/download"
        words = ("releases.example.test",)
        config = {"publish": {"site_url": "https://site.example.test", "update_url": url + "/"}}
        code, output = self.scan(url, words=words, config=config)
        self.assertEqual(code, 0, output)
        for text in (url + "/private", url + "?token=placeholder", "https://releases.example.test/owner",
                     "https://site.example.test/dl", "person@releases.example.test"):
            with self.subTest(text=text):
                code, output = self.scan(text, words=words + ("site.example.test",), config=config)
                self.assertNotEqual(code, 0)
                self.assertIn("private value", output)

    def test_the_app_ssh_algorithm_names_are_not_emails(self):
        names = "\n".join(a + "@" + d for a, d in (("aes256-gcm", "openssh.com"), ("curve25519-sha256", "libssh.org"),
                                                    ("ssh-ed25519-cert-v01", "openssh.com")))
        code, output = self.scan(names, public=False)
        self.assertEqual(code, 0, output)
        comma_names = ",".join(a + "@" + d for a, d in (("aes256-gcm", "openssh.com"), ("curve25519-sha256", "libssh.org"),
                                                         ("ssh-ed25519-cert-v01", "openssh.com")))
        code, output = self.scan(comma_names, public=False)
        self.assertEqual(code, 0, output)
        for other in ("person@" + "openssh.com", "xaes256-gcm@" + "openssh.com", "aes256-gcm@" + "openssh.com.cn"):
            with self.subTest(other=other):
                code, output = self.scan(other, public=False)
                self.assertNotEqual(code, 0)
                self.assertIn("email", output)

    def test_original_field_boundaries_survive_inherent_id_normalization(self):
        config = {"app": {"bundle_id": "public.bundle", "team_id": "PUBLIC_TEAM"}}
        for name in (*SSH_NAMES, ",".join(SSH_NAMES)):
            for value in ("public.bundle", "PUBLIC_TEAM"):
                for text in (value + name, name + value):
                    code, output = self.scan(text, public=False, config=config)
                    self.assertNotEqual(code, 0)
                    self.assertIn("email", output)

    def test_protocol_fields_do_not_hide_unrelated_data_or_registered_values(self):
        for public in (False, True):
            code, output = self.scan("\0".join(SSH_NAMES), public=public)
            self.assertEqual(code, 0, output)
            code, output = self.scan("\0".join((*SSH_NAMES, EMAIL)), public=public)
            self.assertNotEqual(code, 0)
            self.assertIn("email", output)
            self.assertNotIn(EMAIL, output)
            for name in SSH_NAMES:
                code, output = self.scan(name, words=(name,), public=public)
                self.assertNotEqual(code, 0)
                self.assertIn("private value", output)
                self.assertNotIn(name, output)
                if not public:  # Only verified Go dependencies allow names inside surrounding context.
                    code, output = self.scan("mailto:" + name, public=public)
                    self.assertNotEqual(code, 0)
                    self.assertIn("email", output)

    def test_app_mode_stays_strict(self):
        code, output = self.scan(CONTACT + "\n" + str(NETWORKS[0]), public=False)
        self.assertNotEqual(code, 0)
        self.assertIn("email", output)
        self.assertIn("private IP", output)


class MacBundleTests(unittest.TestCase):
    """The Mac app's layout: Contents/MacOS/<binary>, Contents/embedded.provisionprofile, universal binaries."""

    def scan(self, files, words):
        with tempfile.TemporaryDirectory(prefix="bundle-privacy-mac-") as directory:
            for name, data in files.items():
                path = Path(directory) / "Persona.app" / "Contents" / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            output = io.StringIO()
            with patch.object(sys, "argv", ["check_bundle_privacy.py", str(Path(directory) / "Persona.app")]), \
                    patch.object(cbp.pc, "module_paths", return_value=None), \
                    patch.object(cbp.pc, "terms", return_value=(words, set())), \
                    patch.object(cbp.persona_config, "load", return_value={}), redirect_stdout(output):
                try:
                    cbp.main()
                except SystemExit as error:
                    return error.code, output.getvalue()
            return 0, output.getvalue()

    def test_values_in_the_mac_binary_fail_and_the_profile_is_skipped(self):
        value = "private-host-name"
        code, output = self.scan({"MacOS/Persona": b"..." + value.encode(), "embedded.provisionprofile": value.encode()},
                                 (value,))
        self.assertNotEqual(code, 0)
        self.assertIn("Contents/MacOS/Persona", output)
        self.assertNotIn("provisionprofile", output)
        self.assertNotIn(value, output)

    def test_a_universal_binary_is_scanned_past_its_first_slice(self):
        value = "private-host-name"
        fat = b"\xca\xfe\xba\xbe" + b"\0" * 60 + b"second slice " + value.encode()
        code, output = self.scan({"MacOS/Persona": fat}, (value,))
        self.assertNotEqual(code, 0, output)


class BinaryPlistTests(unittest.TestCase):
    address = str(ipaddress.ip_address(0x0A01010B))

    @staticmethod
    def marker(kind, length):
        return bytes([kind << 4 | length]) if length < 15 else bytes([kind << 4 | 15, 0x12]) + struct.pack(">I", length)

    @classmethod
    def string(cls, text, utf16=False):
        data = text.encode("utf-16be" if utf16 else "ascii")
        return cls.marker(6 if utf16 else 5, len(data) // (2 if utf16 else 1)) + data

    @staticmethod
    def plist(objects, width=2, *, before=b"", padding=b"", offset_padding=b"", tail=b""):
        data = bytearray(b"bplist00" + before)
        offsets = []
        for obj in objects:
            offsets.append(len(data))
            data.extend(obj + b"\0")  # Separate object text, rather than creating one enormous printable field.
        data.extend(padding)
        table = len(data)
        data.extend(b"".join(struct.pack(">I", offset) for offset in offsets) + offset_padding)
        return bytes(data) + struct.pack(">6xBBQQQ", 4, width, len(objects), 0, table) + tail

    @classmethod
    def reference_objects(cls, text, kind=10, width=2):
        raw = text.encode()
        raw += b"\0" * (-len(raw) % width)
        refs = [int.from_bytes(raw[i:i + width], "big") for i in range(0, len(raw), width)]
        keys = b"".join(i.to_bytes(width, "big") for i in range(1, len(refs) + 1)) if kind == 13 else b""
        root = cls.marker(kind, len(refs)) + keys + raw
        # Unique string objects make dictionary keys valid too; the large indexes are deliberate, not text values.
        return [root] + [cls.string("item%d" % i) for i in range(1, max(refs) + 1)]

    def scan(self, data, words=()):
        with tempfile.TemporaryDirectory(prefix="bundle-privacy-plist-") as directory:
            path = Path(directory) / "Localizable.strings"
            path.write_bytes(data)
            output = io.StringIO()
            with patch.object(sys, "argv", ["check_bundle_privacy.py", directory]), \
                    patch.object(cbp.pc, "module_paths", return_value=None), \
                    patch.object(cbp.pc, "terms", return_value=(words, set())), \
                    patch.object(cbp.persona_config, "load", return_value={}), redirect_stdout(output):
                try:
                    cbp.main()
                except SystemExit as error:
                    return error.code, output.getvalue()
            return 0, output.getvalue()

    def test_valid_reference_bytes_are_not_generic_addresses(self):
        for kind in (10, 13):
            for width in (1, 2):
                for text in (self.address, EMAIL) if width == 1 else (self.address,):
                    with self.subTest(kind=kind, width=width, length=len(text)):
                        data = self.plist(self.reference_objects(text, kind, width), width)
                        self.assertIsInstance(plistlib.loads(data), list if kind == 10 else dict)
                        ranges, strings = cbp.plist_references(data)
                        self.assertTrue(ranges)
                        self.assertFalse(any(cbp.pc.problems(value, []) for value in strings))
                        self.assertTrue(any(text.encode() in match.group() for match in cbp.PRINTABLE.finditer(data)))
                        code, output = self.scan(data)
                        self.assertEqual(code, 0, output)

    def test_real_ascii_and_utf16_keys_and_values_still_fail(self):
        for utf16 in (False, True):
            for text, kind in ((self.address, "private IP"), (EMAIL, "email")):
                for as_key in (False, True):
                    with self.subTest(utf16=utf16, kind=kind, as_key=as_key):
                        values = (text, "safe") if as_key else ("safe", text)
                        data = self.plist([b"\xd1\x00\x01\x00\x02", *(self.string(v, utf16) for v in values)])
                        self.assertTrue(cbp.plist_references(data)[0])
                        code, output = self.scan(data)
                        self.assertNotEqual(code, 0)
                        self.assertIn(kind, output)
                        self.assertNotIn(text, output)

    def test_unreferenced_strings_and_data_are_not_hidden(self):
        for text, kind in ((self.address, "private IP"), (EMAIL, "email")):
            for obj in (self.string(text), self.string(text, True), self.marker(4, len(text)) + text.encode()):
                with self.subTest(kind=kind, marker=obj[0]):
                    objects = self.reference_objects(self.address) + [obj]
                    data = self.plist(objects)
                    self.assertTrue(cbp.plist_references(data)[0])
                    code, output = self.scan(data)
                    self.assertNotEqual(code, 0)
                    self.assertIn(kind, output)
                    self.assertNotIn(text, output)

    def test_referenced_data_and_padding_and_trailing_bytes_still_fail(self):
        for text, kind in ((self.address, "private IP"), (EMAIL, "email")):
            raw = b"\0" + text.encode() + b"\0"
            objects = self.reference_objects(self.address)
            samples = [self.plist(objects, **{location: raw}) for location in
                       ("before", "padding", "offset_padding", "tail")]
            samples.append(self.plist([b"\xa1\x00\x01", self.marker(4, len(raw)) + raw]))
            for index, data in enumerate(samples):
                with self.subTest(kind=kind, location=index):
                    code, output = self.scan(data)
                    self.assertNotEqual(code, 0)
                    self.assertIn(kind, output)
                    self.assertNotIn(text, output)

    def test_configured_values_still_match_reference_bytes_and_strings(self):
        for text in (self.address, EMAIL, "private-placeholder"):
            reference_data = tuple(self.plist(self.reference_objects(text, width=width), width=width)
                                   for width in ((1, 2) if text == self.address else (1,)))
            for data in (*reference_data, self.plist([self.string(text)]), self.plist([self.string(text, True)])):
                with self.subTest(length=len(text), bytes=len(data)):
                    code, output = self.scan(data, (text,))
                    self.assertNotEqual(code, 0)
                    self.assertIn("private value (%d chars)" % len(text), output)
                    self.assertNotIn(text, output)

    def test_malformed_references_and_objects_do_not_exclude_any_bytes(self):
        objects = self.reference_objects(self.address)
        # Keep the pseudo-address in valid-looking references while invalidating other parts of the structure.
        samples = [self.plist(objects + [b"\xa1\xff\xff"]),  # out-of-range unreachable reference
                   self.plist(objects + [b"\xa1" + len(objects).to_bytes(2, "big")]),  # unreachable self-cycle
                   self.plist(objects + [b"\x7f"]),  # unsupported object
                   self.plist(objects + [b"\x5f\x14"]),  # invalid extended length
                   self.plist(objects + [b"\x5e"]),  # truncated object
                   self.plist(objects + [b"\xd1\x00\x00\x00\x01"])]  # non-string dictionary key
        original = self.plist(objects)
        table = struct.unpack_from(">Q", original, len(original) - 8)[0]
        for offset, replacement in ((len(original) - 26, b"\0"),  # bad offset width
                                    (len(original) - 25, b"\0"),  # bad reference width
                                    (len(original) - 16, struct.pack(">Q", len(objects))),  # bad root
                                    (table + 4, struct.pack(">I", 8)),  # duplicate offsets
                                    (table + 4, struct.pack(">I", 9)),  # overlapping objects
                                    (table + 4, struct.pack(">I", table))):  # out-of-bounds offset
            samples.append(original[:offset] + replacement + original[offset + len(replacement):])
        samples.extend((original[:-1], original + b"trailing"))
        for index, data in enumerate(samples):
            with self.subTest(mutation=index):
                self.assertEqual(cbp.plist_references(data)[0], [])
                code, output = self.scan(data)
                self.assertNotEqual(code, 0)
                self.assertIn("private IP", output)
                self.assertNotIn(self.address, output)

    def test_malformed_plists_and_raw_data_do_not_hide_utf16_addresses(self):
        for text, kind in ((self.address, "private IP"), (EMAIL, "email")):
            raw = text.encode("utf-16be")
            samples = (self.plist([b"\xa1\xff\xff", self.string(text, True)]),
                       b"bplist00" + self.string(text, True),
                       self.plist([b"\xa1\x00\x01", self.marker(4, len(raw)) + raw]),
                       self.plist(self.reference_objects(self.address), padding=raw))
            for index, data in enumerate(samples):
                with self.subTest(kind=kind, location=index):
                    code, output = self.scan(data)
                    self.assertNotEqual(code, 0)
                    self.assertIn(kind, output)
                    self.assertNotIn(text, output)

    def test_binary_plist_scanning_uses_the_supplied_snapshot(self):
        with tempfile.TemporaryDirectory(prefix="bundle-privacy-plist-snapshot-") as directory:
            path = Path(directory) / "Localizable.strings"
            path.write_bytes(self.plist([self.string("safe")]))
            snapshot = self.plist([self.string(self.address, True)])
            self.assertTrue(any("private IP" in cbp.text_kinds(text, ()) for text in cbp.texts(str(path), snapshot)))


class MachOSignatureTests(unittest.TestCase):
    @staticmethod
    def thin(body=b"visible-code", signature=b"certificate-placeholder", *, wide=True, endian="<", tail=b""):
        magic = 0xFEEDFACF if wide else 0xFEEDFACE
        header = struct.pack(endian + "7I", magic, 0, 0, 2, 1, 16, 0)
        if wide:
            header += struct.pack(endian + "I", 0)
        blob = struct.pack(">II", 0xFADE0B01, 8 + len(signature)) + signature
        superblob = struct.pack(">IIIII", 0xFADE0CC0, 20 + len(blob), 1, 0x10000, 20) + blob
        offset = len(header) + 16 + len(body)
        command = struct.pack(endian + "4I", 0x1D, 16, offset, len(superblob))
        return header + command + body + superblob + tail

    @staticmethod
    def fat(slices, *, wide=False, endian=">"):
        count = len(slices)
        entry_size = 32 if wide else 20
        offset = 8 + entry_size * count
        entries = []
        for slice_data in slices:
            fields = (0, 0, offset, len(slice_data), 0)
            entries.append(struct.pack(endian + ("IIQQII" if wide else "5I"), *(fields + (0,) if wide else fields)))
            offset += len(slice_data)
        header = struct.pack(endian + "II", 0xCAFEBABF if wide else 0xCAFEBABE, count)
        return header + b"".join(entries) + b"".join(slices)

    def read_texts(self, data):
        with tempfile.TemporaryDirectory(prefix="macho-signature-test-") as directory:
            path = Path(directory) / "binary"
            path.write_bytes(data)
            return list(cbp.texts(str(path)))

    def test_thin_32_and_64_bit_signatures_both_byte_orders(self):
        for wide in (False, True):
            for endian in ("<", ">"):
                with self.subTest(wide=wide, endian=endian):
                    texts = self.read_texts(self.thin(wide=wide, endian=endian))
                    self.assertTrue(any("visible-code" in text for text in texts))
                    self.assertFalse(any("certificate-placeholder" in text for text in texts))

    def test_each_universal_slice_is_scanned_without_its_signature(self):
        for wide in (False, True):
            for endian in ("<", ">"):
                with self.subTest(wide=wide, endian=endian):
                    data = self.fat([self.thin(b"first-private-value"), self.thin(b"second-private-value", wide=False)],
                                    wide=wide, endian=endian)
                    texts = self.read_texts(data)
                    self.assertIn("first-private-value", texts)
                    self.assertIn("second-private-value", texts)
                    self.assertFalse(any("certificate-placeholder" in text for text in texts))

    def test_trailing_slice_and_file_data_is_not_hidden(self):
        data = self.fat([self.thin(tail=b"slice-private-value"), self.thin(wide=False)]) + b"file-private-value"
        texts = self.read_texts(data)
        self.assertIn("slice-private-value", texts)
        self.assertIn("file-private-value", texts)

    def test_signature_padding_is_still_scanned(self):
        data = bytearray(self.thin(tail=b"padding-private-value"))
        signature_size = struct.unpack_from("<I", data, 44)[0]
        struct.pack_into("<I", data, 44, signature_size + len(b"padding-private-value"))
        self.assertIn("padding-private-value", self.read_texts(bytes(data)))

    def test_bad_signature_commands_are_not_excluded(self):
        original = self.thin(signature=b"private-signature-value")
        mutations = ((36, 8), (40, 0), (40, len(original) + 1), (44, len(original) + 1))
        for offset, value in mutations:
            with self.subTest(offset=offset, value=value):
                data = bytearray(original)
                struct.pack_into("<I", data, offset, value)
                self.assertFalse(cbp.signature_ranges(bytes(data)))
                self.assertTrue(any("private-signature-value" in text for text in self.read_texts(bytes(data))))

    def test_bad_superblob_is_not_excluded(self):
        original = self.thin(signature=b"private-signature-value")
        start = struct.unpack_from("<I", original, 40)[0]
        for offset, value in ((start, 0), (start + 4, len(original) + 1), (start + 8, 100),
                              (start + 16, 0), (start + 24, len(original) + 1)):
            with self.subTest(offset=offset):
                data = bytearray(original)
                struct.pack_into(">I", data, offset, value)
                self.assertFalse(cbp.signature_ranges(bytes(data)))
                self.assertTrue(any("private-signature-value" in text for text in self.read_texts(bytes(data))))

    def test_overlapping_or_out_of_bounds_slices_are_not_excluded(self):
        original = self.fat([self.thin(signature=b"first-private-signature"), self.thin(signature=b"second-private-signature")])
        first_offset = struct.unpack_from(">I", original, 16)[0]
        for offset, value in ((16, 0), (20, len(original) + 1), (36, first_offset)):
            with self.subTest(offset=offset):
                data = bytearray(original)
                struct.pack_into(">I", data, offset, value)
                self.assertFalse(cbp.signature_ranges(bytes(data)))
                texts = self.read_texts(bytes(data))
                self.assertTrue(any("first-private-signature" in text for text in texts))
                self.assertTrue(any("second-private-signature" in text for text in texts))

    def test_truncated_inputs_never_skip_or_crash(self):
        data = self.fat([self.thin(), self.thin(wide=False)])
        for length in range(len(data)):
            with self.subTest(length=length):
                self.assertEqual(cbp.signature_ranges(data[:length]), [])


if __name__ == "__main__":
    unittest.main()
