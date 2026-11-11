#!/usr/bin/env python3
"""Fail when a built app bundle holds this person's private values: anyone with the TestFlight public link can install it.

    python3 scripts/release/check_bundle_privacy.py <Persona.app>     # the watch app and extensions inside are scanned too
    python3 scripts/release/check_bundle_privacy.py <Mac Persona.app> # the Mac app (Contents/…), same rules
    python3 scripts/release/check_bundle_privacy.py --whole-short <dir>  # published personad builds (scripts/release/publish.sh)

The values are scripts/privacy_check.py's (config, private-words.txt, CLAUDE.local.md, the home directory), plus private
IPv4 addresses and emails outside placeholder domains, looked for in every file's printable strings (like `strings`,
after decompressing lzfse files) and in binary plists. The bundle id and team id are in every signed build on purpose
and are removed before matching. App mode also recognizes only the exact configured public HTTPS /relay endpoint, and
--whole-short only the exact public download address (publish.site_url + /dl, the hub's update URL, docs/operations/hub-update.md);
their host in any other context, related paths and credentials remain subject to private-value checks.
Signing data is Apple's and is skipped: embedded.mobileprovision (embedded.provisionprofile on the Mac), _CodeSignature,
and each Mach-O slice's validated embedded-signature blob (certificate names; the archive's development profile lists
device ids, which export replaces anyway). A universal (fat) binary's remaining bytes are scanned across every slice;
its signature offsets are relative to its slice, and cutting at the first would skip the rest of the file.
Values match as written or lowercased, not in any case: Swift's mangled symbol names are full of short fragments
that equal a value in another case.
The repo's own Go module path (it names the GitHub owner) is in every Go binary's imports and is removed too, as
scripts/privacy_check.py does for the sources. --whole-short: a value of four characters or fewer only counts as a
whole word, not inside a longer one (a Go binary carries Unicode tables and minified JavaScript, where short values turn
up inside ordinary words); app bundles keep the strict substring match except for short alphabetic fragments inside
word tokens of the exact SHA-256-pinned, unmodified public Mermaid resource. Complete words, nonalphabetic values,
long values and generic IP/email checks remain active there; any changed byte falls back to strict app matching.
--whole-short also reads every gzip member inside a file: a Go build embeds code compressed (the runtime packages,
server/internal/runtimepkg, are gzipped tars of JavaScript; the web client's hashed assets are stored gzipped), and
its printable strings are noise until it is inflated. Each member is scanned like the rest of the file, besides the
file's own bytes.
Prints the file and the kind of hit, never the value.
For public Go builds, narrowly scoped dependency constants are classified separately from private data. This requires
a build with exactly the tags of server/build-tags.txt (the public marker and ts_omit_ feature tags), and unreplaced
dependencies whose build-info checksums match server/go.sum; registered personal values always win.
The seven known swift-nio-ssh algorithm/certificate names are also classified as a complete original printable field
or a complete comma-separated list; that fallback excludes URLs, surrounding context and normalized fragments.
"""
import hashlib
import ipaddress
import itertools
import os
import re
import struct
import subprocess
import sys
import zlib
from urllib.parse import urlsplit

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import persona_config  # noqa: E402
import privacy_check as pc  # noqa: E402

SKIP = {"embedded.mobileprovision", "embedded.provisionprofile", "CodeResources"}
PRINTABLE = re.compile(rb"[\x20-\x7e]{4,}")
PLIST_UTF16 = re.compile(rb"(?:\x00[\x20-\x7e]){4,}")

# npm mermaid@12.3.0, package/dist/mermaid.min.js; verified against the registry tarball's SHA-512 integrity.
PUBLIC_JS_SHA256 = {
    "mermaid.min.js": "336df803d87a38b70a01e736e666c8f7601d2ce42f5e47c8fafe5d721074b9a3",
}


def verified_public_js(path, data):
    """Use the same byte snapshot for provenance and scanning, not a second file read or a mutable manifest."""
    digest = PUBLIC_JS_SHA256.get(os.path.basename(path))
    return digest is not None and hashlib.sha256(data).hexdigest() == digest


# These are protocol and standards constants, not accounts or deployment addresses. Keep contexts narrow:
# SSH wire identifiers, the upstream web client's exact contact link, and complete RFC 1918 networks.
# Exact names from x/crypto/ssh, gliderssh and pkg/sftp; never exempt either domain as a whole.
GO_SSH_IDENTIFIERS = re.compile(
    r"(?<![A-Za-z0-9._%+-])(?:"
    r"(?:aes128-gcm|aes256-gcm|chacha20-poly1305|"
    r"hmac-sha2-256-etm|hmac-sha2-512-etm|"
    r"rsa-sha2-256-cert-v01|rsa-sha2-512-cert-v01|ssh-rsa-cert-v01|ssh-dss-cert-v01|ssh-ed25519-cert-v01|"
    r"ecdsa-sha2-nistp256-cert-v01|ecdsa-sha2-nistp384-cert-v01|ecdsa-sha2-nistp521-cert-v01|"
    r"sk-ssh-ed25519|sk-ssh-ed25519-cert-v01|sk-ecdsa-sha2-nistp256|sk-ecdsa-sha2-nistp256-cert-v01|"
    r"kex-strict-c-v00|kex-strict-s-v00|ping|auth-agent-req|auth-agent|"
    r"direct-streamlocal|forwarded-streamlocal|streamlocal-forward|cancel-streamlocal-forward|keepalive|"
    r"statvfs|fstatvfs|fsync|hardlink|posix-rename)@openssh[.]com|"
    r"curve25519-sha256@libssh[.]org)(?![A-Za-z0-9._%+-])"
)
# Fixed wire names from swift-nio-ssh. A whole field may be one name or a complete known name-list, never a URL.
SSH_ALGORITHM_NAMES = frozenset(name + "@" + domain for name, domain in (
    ("aes128-gcm", "openssh.com"), ("aes256-gcm", "openssh.com"), ("curve25519-sha256", "libssh.org"),
    ("ecdsa-sha2-nistp256-cert-v01", "openssh.com"), ("ecdsa-sha2-nistp384-cert-v01", "openssh.com"),
    ("ecdsa-sha2-nistp521-cert-v01", "openssh.com"), ("ssh-ed25519-cert-v01", "openssh.com"),
))
TAILSCALE_CONTACT = re.compile(r'<a href="mailto:support\+webclient@tailscale[.]com" class="link">contact us</a>')
RFC1918_NETWORKS = tuple(ipaddress.ip_network((address, prefix)) for address, prefix in (
    (0x0A000000, 8), (0xAC100000, 12), (0xC0A80000, 16),
))
# RFC 3927 link-local address used by AWS, Google Cloud and Azure for instance metadata.
CLOUD_METADATA_IP = ipaddress.ip_address(0xA9FEA9FE)


def public_build_tags(tags_path=None):
    """The -tags of a published build (server/build-tags.txt): the public marker plus tailscale.com's ts_omit_ feature
    tags, each once. A file with any other tag, or none, marks no build as public."""
    try:
        with open(tags_path or os.path.join(pc.ROOT, "server", "build-tags.txt")) as f:
            tags = f.read().strip().split(",")
    except OSError:
        return None
    if "public" not in tags or len(set(tags)) != len(tags) or not all(
            tag == "public" or re.fullmatch(r"ts_omit_[a-z0-9_]+", tag) for tag in tags):
        return None
    return tags


def go_dependencies(path, sums_path=None, tags_path=None):
    """Only classify constants from unreplaced modules recorded in both this Go executable and go.sum, in a build
    with exactly the published tags."""
    sums_path = sums_path or os.path.join(pc.ROOT, "server", "go.sum")
    public_tags = public_build_tags(tags_path)
    if public_tags is None:
        return set()
    try:
        with open(sums_path) as sums:
            trusted = {tuple(line.split()) for line in sums if len(line.split()) == 3}
        result = subprocess.run(["go", "version", "-m", path], capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return set()
    if result.returncode:
        return set()
    dependencies = set()
    previous = None
    public_build = False
    for line in result.stdout.splitlines():
        fields = line.split()
        if fields == ["build", "-tags=" + ",".join(public_tags)]:
            public_build = True
        if fields[:1] == ["dep"] and len(fields) == 4:
            previous = fields[1]
            if tuple(fields[1:]) in trusted:
                dependencies.add(previous)
        elif fields[:1] == ["=>"]:
            dependencies.discard(previous)
        elif fields[:1] in (["mod"], ["dep"]):
            previous = None
    return dependencies if public_build else set()


def binary_noise_email(domain):
    """Machine code read as text, not an address (#4497): a two-letter mixed-case country domain ("Sg") or a
    second-level label with no letter ("9"). Longer mixed-case tails ("comA") still count: a real name runs into them."""
    labels = domain.split(".")
    tld, sld = labels[-1], labels[-2]
    return (len(tld) == 2 and tld != tld.lower() and tld != tld.upper()) or not any(c.isalpha() for c in sld)


def generic_text(text, dependencies, original_text=None):
    """Classify public constants only for generic IP/email detection, never private-word matching."""
    if original_text is None or text == original_text:
        if text in SSH_ALGORITHM_NAMES or ("," in text and all(name in SSH_ALGORITHM_NAMES for name in text.split(","))):
            return ""
    text = pc.EMAIL.sub(lambda m: "" if binary_noise_email(m.group(1)) else m.group(0), text)
    if "golang.org/x/crypto" in dependencies:
        text = GO_SSH_IDENTIFIERS.sub("", text)
    if "github.com/tailscale/web-client-prebuilt" in dependencies:
        text = TAILSCALE_CONTACT.sub("", text)
    if "tailscale.com" not in dependencies:
        return text

    def public_address(match):
        address = match.group(1)
        try:
            ip = ipaddress.ip_address(address)
        except ValueError:
            return match.group(0)
        suffix = text[match.end():]
        for network in RFC1918_NETWORKS:
            if ip != network.network_address:
                continue
            if re.match(r"/%d(?!\d)" % network.prefixlen, suffix):
                return ""
            # ipnlocal's private and link-local CIDRs are consecutive Go string constants.
            link_local = str(ipaddress.ip_network((0xA9FE0000, 16)))
            if network.prefixlen == 16 and suffix.startswith("/16" + link_local):
                return ""
        if ip == CLOUD_METADATA_IP:
            # Go concatenates string constants in rodata, so the HTTP literal can touch adjacent strings.
            if text[max(0, match.start() - 7):match.start()] == "http://":
                return ""
            # cloudenv's adjacent metadata/resolver constants provide a specific non-URL context.
            resolver = str(ipaddress.ip_address(int(CLOUD_METADATA_IP) - 1))
            if suffix.startswith(resolver + "Metadata-Flavor"):
                return ""
        return match.group(0)

    return pc.IPV4.sub(public_address, text)


def text_kinds(text, words, whole_short=False, dependencies=frozenset(), original_text=None, public_js=False,
               generic=True):
    kinds = set()
    for word in words:
        pinned_short = (public_js and (original_text is None or text == original_text)
                        and len(word) <= 4 and word.isascii() and word.isalpha())
        if pinned_short:
            if re.search(r"(?<![A-Za-z0-9_$])(%s|%s)(?![A-Za-z0-9_$])" %
                         (re.escape(word), re.escape(word.lower())), text):
                kinds.add("private value (%d chars)" % len(word))
        elif whole_short and len(word) <= 4:
            if re.search(r"(?<![A-Za-z])(%s|%s)(?![A-Za-z])" % (re.escape(word), re.escape(word.lower())), text):
                kinds.add("private value (%d chars)" % len(word))
        elif word in text or word.lower() in text:
            kinds.add("private value (%d chars)" % len(word))
    if generic:
        for word in pc.problems(generic_text(text, dependencies, original_text), []):
            kinds.add("private IP" if pc.IPV4.fullmatch(word) else "email")
    return kinds


THIN_HEADERS = {b"\xce\xfa\xed\xfe": ("<", 28), b"\xfe\xed\xfa\xce": (">", 28),
                b"\xcf\xfa\xed\xfe": ("<", 32), b"\xfe\xed\xfa\xcf": (">", 32)}
FAT_HEADERS = {b"\xca\xfe\xba\xbe": (">", False), b"\xbe\xba\xfe\xca": ("<", False),
               b"\xca\xfe\xba\xbf": (">", True), b"\xbf\xba\xfe\xca": ("<", True)}


def signature_blob_length(data, start, size):
    """Accept only a bounded embedded-signature SuperBlob; its padding is still scanned."""
    if size < 12:
        return None
    magic, length, count = struct.unpack_from(">III", data, start)
    if magic != 0xFADE0CC0 or not 12 <= length <= size or not 0 < count <= (length - 12) // 8:
        return None
    table_end = 12 + count * 8
    blobs = []
    for i in range(count):
        _, offset = struct.unpack_from(">II", data, start + 12 + i * 8)
        if offset < table_end or offset + 8 > length:
            return None
        blob_magic, blob_size = struct.unpack_from(">II", data, start + offset)
        if blob_magic not in (0xFADE0C01, 0xFADE0C02, 0xFADE0B01, 0xFADE7171, 0xFADE7172):
            return None
        if blob_size < 8 or offset + blob_size > length:
            return None
        blobs.append((offset, offset + blob_size))
    blobs.sort()
    if any(a[1] > b[0] for a, b in zip(blobs, blobs[1:])):
        return None
    return length


def thin_signature_ranges(data, base, size):
    """Read slice-relative load commands. None means malformed: do not exclude any bytes."""
    spec = THIN_HEADERS.get(data[base:base + 4])
    if spec is None:
        return None
    endian, header_size = spec
    if size < header_size:
        return None
    commands, command_bytes = struct.unpack_from(endian + "II", data, base + 16)
    commands_end = base + header_size + command_bytes
    if commands > command_bytes // 8 or commands_end > base + size:
        return None
    cursor = base + header_size
    ranges = []
    for _ in range(commands):
        if cursor + 8 > commands_end:
            return None
        command, command_size = struct.unpack_from(endian + "II", data, cursor)
        if command_size < 8 or command_size % 4 or cursor + command_size > commands_end:
            return None
        if command == 0x1D:  # LC_CODE_SIGNATURE
            if command_size != 16 or ranges:
                return None
            offset, signature_size = struct.unpack_from(endian + "II", data, cursor + 8)
            start = base + offset
            if start < commands_end or signature_size > base + size - start:
                return None
            length = signature_blob_length(data, start, signature_size)
            if length is None:
                return None
            ranges.append((start, start + length))
        cursor += command_size
    return ranges if cursor == commands_end else None


def signature_ranges(data):
    """Validated signature ranges of every thin / universal 32- or 64-bit Mach-O slice."""
    if data[:4] in THIN_HEADERS:
        return thin_signature_ranges(data, 0, len(data)) or []
    spec = FAT_HEADERS.get(data[:4])
    if spec is None or len(data) < 8:
        return []
    endian, wide = spec
    count = struct.unpack_from(endian + "I", data, 4)[0]
    arch_size = 32 if wide else 20
    if not 0 < count <= (len(data) - 8) // arch_size:
        return []
    table_end = 8 + count * arch_size
    slices = []
    for i in range(count):
        entry = 8 + i * arch_size
        offset, size = struct.unpack_from(endian + ("QQ" if wide else "II"), data, entry + 8)
        if offset < table_end or size < 28 or offset + size > len(data):
            return []
        slices.append((offset, offset + size))
    slices.sort()
    if any(a[1] > b[0] for a, b in zip(slices, slices[1:])):
        return []
    ranges = []
    for start, end in slices:
        found = thin_signature_ranges(data, start, end - start)
        if found is None:
            return []
        ranges.extend(found)
    return ranges


def plist_references(data):
    """Only validated array/dictionary reference payloads are non-text (#3166).

    Check every indexed object, including unreachable ones, before trusting any range. Unknown types, overlaps,
    bad lengths/references and cycles fall back to raw scanning. Data, scalar payloads, gaps and offset/trailer bytes
    are never excluded. Decode every string object too: plutil's root traversal misses unreachable UTF-16 strings.
    """
    if data[:8] != b"bplist00" or len(data) < 40:
        return [], []
    offset_size, ref_size, count, root, table = struct.unpack_from(">6xBBQQQ", data, len(data) - 32)
    if (data[-32:-26] != b"\0" * 6 or offset_size not in (1, 2, 4, 8) or ref_size not in (1, 2, 4, 8)
            or not 0 < count <= (len(data) - 32 - table) // offset_size or root >= count or table < 8
            or count > 1 << (8 * ref_size)):
        return [], []
    offsets = [int.from_bytes(data[table + i * offset_size:table + (i + 1) * offset_size], "big")
               for i in range(count)]
    if len(set(offsets)) != count or any(not 8 <= offset < table for offset in offsets):
        return [], []
    spans, ranges, strings = [], [], {}
    references = [[] for _ in offsets]
    dictionaries = []
    try:
        for index, offset in enumerate(offsets):
            marker = data[offset]
            kind, length = marker >> 4, marker & 15
            start = offset + 1
            if kind in (4, 5, 6, 10, 13) and length == 15:
                if start >= table or data[start] >> 4 != 1 or data[start] & 15 > 3:
                    return [], []
                width = 1 << (data[start] & 15)
                start += 1
                if start + width > table:
                    return [], []
                length = int.from_bytes(data[start:start + width], "big")
                start += width
            if kind == 0 and marker in (0, 8, 9, 15):
                size = 0
            elif kind == 1 and length <= 4:
                size = 1 << length
            elif (kind == 2 and length in (2, 3)) or marker == 0x33:
                size = 1 << length
            elif kind in (4, 5, 6):
                size = length * (2 if kind == 6 else 1)
            elif kind == 8:
                size = length + 1
            elif kind in (10, 13):
                size = length * ref_size * (2 if kind == 13 else 1)
            else:
                return [], []
            end = start + size
            if end > table:
                return [], []
            spans.append((offset, end))
            if kind in (5, 6):
                strings[index] = data[start:end].decode("ascii" if kind == 5 else "utf-16be")
            elif kind in (10, 13):
                refs = [int.from_bytes(data[pos:pos + ref_size], "big") for pos in range(start, end, ref_size)]
                if any(ref >= count for ref in refs):
                    return [], []
                references[index] = refs
                if size:
                    ranges.append((start, end))
                if kind == 13:
                    dictionaries.append(refs[:length])
    except UnicodeDecodeError:
        return [], []
    spans.sort()
    if any(a[1] > b[0] for a, b in zip(spans, spans[1:])):
        return [], []
    for keys in dictionaries:
        if any(key not in strings for key in keys) or len({strings[key] for key in keys}) != len(keys):
            return [], []
    # Iterative traversal bounds work by the object/reference count, not Python's recursion limit.
    states = [0] * count
    for index in range(count):
        stack = [(index, False)]
        while stack:
            index, exiting = stack.pop()
            if exiting:
                states[index] = 2
            elif states[index] == 1:
                return [], []
            elif states[index] == 0:
                states[index] = 1
                stack.append((index, True))
                stack.extend((ref, False) for ref in references[index])
    return sorted(ranges), list(strings.values())


def texts(path: str, data=None, *, private_only=False):
    if data is None:
        with open(path, "rb") as source:
            data = source.read()
    if data[:3] == b"bvx":  # lzfse (App Intents metadata): compressed, values would be split across matches
        data = subprocess.run(["compression_tool", "-decode", "-a", "lzfse", "-i", path, "-o", "/dev/stdout"],
                              capture_output=True).stdout
    references, strings = plist_references(data) if not private_only else ([], [])
    binary_plist = data[:6] == b"bplist"
    # Do not truncate at the first slice's signature: later slices and trailing bytes must remain visible.
    cursor = 0
    for start, end in sorted(signature_ranges(data) + references) + [(len(data), len(data))]:
        chunk = data[cursor:start]
        yield from (m.group(0).decode("ascii") for m in PRINTABLE.finditer(chunk))
        if binary_plist:
            # Keep ASCII IP/email text in UTF-16 data, gaps or malformed/unreachable objects visible too.
            yield from (m.group(0).decode("utf-16be") for m in PLIST_UTF16.finditer(chunk))
        cursor = end
    yield from strings
    if binary_plist and not private_only:
        yield subprocess.run(["plutil", "-convert", "xml1", "-o", "-", path], capture_output=True, text=True).stdout


GZIP_MAGIC = b"\x1f\x8b\x08"
# A member's output bound: far above any package (runtimepkg.MaxSize, 8 MiB) or web asset, and a cap on a bomb.
GZIP_MEMBER_LIMIT = 64 << 20
GZIP_CHUNK = 1 << 20


def gzip_members(data, limit=GZIP_MEMBER_LIMIT):
    """The inflated bytes of each gzip member in data, wherever it starts. A magic that starts no valid stream yields
    nothing; a member cut short, broken off or longer than limit yields what it inflated, so its text is still scanned.
    The search goes on after a complete member, else from the next byte."""
    view = memoryview(data)
    start = data.find(GZIP_MAGIC)
    while start >= 0:
        inflater = zlib.decompressobj(16 + zlib.MAX_WBITS)
        pieces, size, pos, end = [], 0, start, None
        try:
            while pos < len(data) and size < limit and not inflater.eof:
                chunk = view[pos:pos + GZIP_CHUNK]
                pos += len(chunk)
                while chunk and size < limit and not inflater.eof:
                    piece = inflater.decompress(chunk, limit - size)
                    pieces.append(piece)
                    size += len(piece)
                    chunk = inflater.unconsumed_tail
            if inflater.eof:
                end = pos - len(inflater.unused_data)
        except zlib.error:
            pass
        if size:
            yield b"".join(pieces)
        start = data.find(GZIP_MAGIC, end if end is not None else start + 1)


def member_texts(data):
    """Printable strings of every gzip member in data, read as the file's own bytes are."""
    for member in gzip_members(data):
        yield from (m.group(0).decode("ascii") for m in PRINTABLE.finditer(member))


def remove_public_relay_url(text, configured_url, path="/relay"):
    """The app intentionally ships this exact public HTTPS endpoint, not its entire host or other URLs. path: the one
    path the endpoint may have (/relay for the push relay, /dl for the hub's update URL in a published personad)."""
    if not isinstance(configured_url, str):
        return text
    url = configured_url.rstrip("/")
    try:
        parsed = urlsplit(url)
        if (parsed.scheme != "https" or not parsed.hostname or "." not in parsed.hostname
                or parsed.username or parsed.password or parsed.query or parsed.fragment
                or parsed.port is not None or parsed.path != path):
            return text
        try:
            ipaddress.ip_address(parsed.hostname)
            return text
        except ValueError:
            pass
    except ValueError:
        return text
    # Do not remove a prefix of another URL, a credential URL, or an extended path/query.
    boundary = r"[A-Za-z0-9._~:/?#@!$&()*+,;=%-]"
    return re.sub(r"(?<!" + boundary + ")" + re.escape(url) + r"(?!" + boundary + ")", "", text)


def public_download_url(cfg):
    """The update URL every published personad carries and the one path it may have: publish.update_url as configured
    (a GitHub repository's releases/latest/download, say), else publish.site_url's /dl (persona_config.update_url)."""
    url = persona_config.update_url(cfg)
    if url and persona_config.get(cfg, "publish.update_url", "") not in ("", None):
        return url, urlsplit(url).path
    return url, "/dl"


def main():
    args = sys.argv[1:]
    whole_short = "--whole-short" in args
    args = [a for a in args if a != "--whole-short"]
    if len(args) != 1 or not os.path.isdir(args[0]):
        sys.exit(__doc__)
    root = args[0]
    modules = pc.module_paths()
    cfg = persona_config.load()
    inherent = [v for v in (persona_config.get(cfg, "app.bundle_id", ""), persona_config.get(cfg, "app.team_id", "")) if v]
    words, _ = pc.terms()
    words = [w for w in words if w not in inherent]
    # Hermes stores the same pinned public resource as strings in the main executable, not as a .js file.
    # Its longer JavaScript tokens use the resource's existing short-word boundary; every other binary stays strict.
    hermes_public_js = not whole_short and os.path.isfile(os.path.join(root, "Persona")) and any(
        verified_public_js(os.path.join(dirpath, filename), open(os.path.join(dirpath, filename), "rb").read())
        for dirpath, _, filenames in os.walk(root) for filename in filenames)
    bad = []
    for dirpath, dirnames, files in os.walk(root):
        dirnames[:] = [d for d in dirnames if d != "_CodeSignature"]
        for f in files:
            if f in SKIP:
                continue
            path = os.path.join(dirpath, f)
            kinds = set()
            dependencies = go_dependencies(path) if whole_short else set()
            with open(path, "rb") as source:
                data = source.read()
            public_js = (not whole_short and verified_public_js(path, data)) or (
                hermes_public_js and f == "Persona")
            # Reference bytes can spell a configured value too; only generic IP/email detection skips them.
            modes = (False, True) if data[:6] == b"bplist" or data[:3] == b"bvx" else (False,)
            for private_only in modes:
                found = texts(path, data, private_only=private_only)
                if whole_short and not private_only:
                    found = itertools.chain(found, member_texts(data))
                for t in found:
                    original_text = t
                    if not whole_short:
                        t = remove_public_relay_url(t, persona_config.get(cfg, "app.push_relay", ""))
                    else:  # the update URL every published personad carries (-X main.updateURL, scripts/release/publish.sh)
                        t = remove_public_relay_url(t, *public_download_url(cfg))
                    for v in inherent:
                        t = t.replace(v, "")
                    if modules:
                        t = modules.sub("", t)
                    kinds.update(text_kinds(t, words, whole_short, dependencies, original_text, public_js=public_js,
                                            generic=not private_only))
            bad += [(os.path.relpath(path, root), k) for k in sorted(kinds)]
    for f, k in bad:
        print("%s: %s" % (f, k))
    if bad:
        sys.exit("check_bundle_privacy: private values in %s (scripts/release/check_bundle_privacy.py)" % root)
    print("check_bundle_privacy: clean (%d values checked)" % len(words))


if __name__ == "__main__":
    main()
