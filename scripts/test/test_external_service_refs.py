"""Tracked files must not point into another nwverse repository that Persona only reaches as an external service.

Persona talks to some services the way it talks to the GitHub API: through their public contract
(OpenAPI, public docs, error codes). A doc, test, prompt or fixture that names another repository's issue (org/repo
followed by #n), links it, or writes a path inside another checkout (a placeholder
followed by its integrations or test fixtures directory) ties Persona to that repository's layout and points readers at
code they are not meant to see. This check makes the rule mechanical. The self-tests build their samples from pieces so
this file passes its own scan.

Only the organisation name on its own is fine (`"publisher": "nwverse"` in a manifest). Repositories Persona openly
builds on are listed in ALLOWED_REPOS with the reason; adding one there is a deliberate, reviewed decision. No word list of
any service's internals lives here: such a list would itself describe what it guards.
"""
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from compatibility_identifiers import registered_external_ref


ROOT = Path(__file__).resolve().parents[2]

# The organisation followed by a slash and any repository other than Persona's own: issue refs, bare URLs, blob links.
REPO_REF = re.compile(r"\bnwverse/(?!persona\b)([\w.-]+)", re.IGNORECASE)
# A placeholder for another checkout followed by its integrations or test fixtures directory.
PATH_PLACEHOLDER = re.compile(r"<[a-z-]+>/(?:integrations|test/fixtures)/")
# A complete, existing path in this checkout is not an external repository name.
REPOSITORY_PATH = re.compile(r"(?<![\w/])(?:server|apple|android|site|web|schema|sdk|integrations|scripts)/[\w./-]+")

# Repositories Persona depends on in the open; the docs and code cite them as sources.
ALLOWED_REPOS = {
    "contribution": "the public contribution board Persona tiers are read from (persona_tiers.go)",
}
# Not repository references: keys in the hub's own Persona file store.
ALLOWED_IN_FILE = {
    ("server/internal/hub/persona_tiers_test.go", "tiers"),
    ("server/internal/hub/persona_tiers_test.go", "dot"),
    # pet: the built-in Milk Frog's art in the organisation's persona package, which the watch's answer-only token reads
    # (#4910, #5069).
    ("server/internal/hub/devicetokens.go", "pet"),
    ("server/internal/hub/devicetokens_test.go", "pet"),
    ("docs/protocol/protocol.md", "pet"),
}


def local_repository_path(path):
    candidate = ROOT / path.rstrip(".,;")
    try:
        candidate.resolve().relative_to(ROOT.resolve())
    except (OSError, ValueError):
        return False
    return candidate.exists()


def hits(text, path="", registry=None):
    """Return (line number, finding) for every external repository reference in text."""
    found = []
    for number, line in enumerate(text.splitlines(), 1):
        local_spans = [match.span() for match in REPOSITORY_PATH.finditer(line)
                       if local_repository_path(match.group(0))]
        for match in REPO_REF.finditer(line):
            if any(start <= match.start() and match.end() <= end for start, end in local_spans):
                continue
            name = match.group(1).rstrip(".").lower()
            if (name not in ALLOWED_REPOS and (path, name) not in ALLOWED_IN_FILE
                    and not registered_external_ref(path, match.group(0), registry)):
                found.append((number, match.group(0)))
        for pattern in (PATH_PLACEHOLDER,):
            found += [(number, match.group(0)) for match in pattern.finditer(line)]
    return found


def tracked_text_files():
    names = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, check=True, capture_output=True).stdout.split(b"\0")
    for name in filter(None, names):
        path = ROOT / name.decode()
        if not path.is_file() or path.is_symlink():
            continue
        data = path.read_bytes()
        if b"\0" in data[:8192]:  # binary
            continue
        yield path.relative_to(ROOT).as_posix(), data.decode("utf-8", errors="ignore")


class ExternalServiceRefTests(unittest.TestCase):
    def test_another_repository_reference_is_rejected(self):
        # Split so the repository scan of this file does not flag it.
        org = "nwverse"
        self.assertEqual([(1, org + "/example-service")], hits("see " + org + "/example-service#1"))
        self.assertEqual(1, len(hits("https://github.com/" + org + "/example-service/blob/main/x.md")))
        self.assertEqual(1, len(hits("PROGRAM=<service>" + "/integrations/plugin/bin/run")))

    def test_organisation_name_and_own_repository_pass(self):
        org = "nwverse"
        self.assertEqual([], hits('"publisher": "' + org + '"'))
        self.assertEqual([], hits("github.com/" + org + "/persona/server"))
        self.assertEqual([], hits("see #1420 and the service's public API reference"))

    def test_a_persona_store_key_passes_only_where_it_is_listed(self):
        org = "nwverse"
        route = "GET /api/v1/personas/" + org + "/pet/pet.json"
        self.assertEqual([], hits(route, path="server/internal/hub/devicetokens.go"))
        self.assertEqual([(1, org + "/pet")], hits(route, path="server/internal/hub/other.go"))

    def test_existing_local_paths_are_not_repository_references(self):
        org = "nwverse"
        relative = "server/testdata/" + org + "/example-art/frames"
        with tempfile.TemporaryDirectory(prefix="service-ref-paths-") as directory:
            root = Path(directory)
            (root / relative).mkdir(parents=True)
            with patch(__name__ + ".ROOT", root):
                self.assertEqual([], hits('default="' + relative + '"'))
                self.assertEqual(1, len(hits(relative + "/missing")))
                self.assertEqual(1, len(hits("https://github.com/" + org + "/example-art")))
                self.assertEqual(1, len(hits(relative + " and " + org + "/example-service#1")))
            with tempfile.TemporaryDirectory(prefix="service-ref-outside-", dir=root.parent) as outside_dir:
                outside = Path(outside_dir)
                (outside / org / "example-art").mkdir(parents=True)
                (root / "server/link").symlink_to(outside, target_is_directory=True)
                with patch(__name__ + ".ROOT", root):
                    self.assertEqual(1, len(hits("server/link/" + org + "/example-art")))
                    traversal = "server/../../" + outside.name + "/" + org + "/example-art"
                    self.assertEqual(1, len(hits(traversal)))

    def test_tracked_files_have_no_external_repository_refs(self):
        found = [f"{path}:{number}: {ref}" for path, text in tracked_text_files() for number, ref in hits(text, path)]
        self.assertEqual([], found, "cite the service's public contract (OpenAPI, public docs, error codes) instead")


if __name__ == "__main__":
    unittest.main()
