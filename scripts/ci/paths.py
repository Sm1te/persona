#!/usr/bin/env python3
import argparse
import fnmatch
import json
import os
from pathlib import Path
import subprocess

# Go compatibility tests read these client contracts and interaction entry points,
# not unrelated Mac/Android screens or their build toolchains.
SERVER_CLIENT_INPUTS = [
    "apple/Shared/**", "apple/Persona/Views/Interactions/**", "apple/Persona/App/**",
    "apple/Persona/Intents/**", "apple/PersonaWatch/**", "apple/PersonaWidgets/**",
    "apple/LiveActivity/**", "apple/PersonaTests/**", "android/core/contracts/**",
]

FILTERS = {
    "server": ["server/**", "sdk/go/**", "sdk/ts/src/**", "schema/**", "spec/**", "integrations/**", "docs/assistant/prompts.md", "docs/protocol/**", "scripts/test/server_test_env.py", "scripts/ci/*server*", "scripts/ci/postgres.py", "scripts/ci/gofmt.py", ".github/workflows/server-test.yml"] + SERVER_CLIENT_INPUTS,
    "web": ["web/**", "schema/extension/**", "spec/fixtures/injected/**", "spec/fixtures/sync/**", "spec/fixtures/attention/**", "scripts/mock/inbox_events.json", "scripts/ci/web.sh", ".github/workflows/web-test.yml"],
    "android": ["android/**", "schema/**", "spec/**", "scripts/app/*android*", "scripts/ci/*android*", ".github/workflows/android-test.yml"],
    "apple": ["apple/**", "schema/**", "spec/**", "scripts/app/sim-boot.sh", "scripts/i18n/**", "scripts/ci/apple*", ".github/workflows/apple-test.yml"],
    "runtimes": ["integrations/**", "sdk/ts/**", "schema/**", "spec/**", "scripts/ci/runtimes.sh", ".github/workflows/runtimes-test.yml"],
    "scripts": ["**"],
    "image": ["Dockerfile", ".dockerignore", "deploy/docker/**", "server/**", "sdk/go/**", "web/**", "integrations/**", "schema/extension/**", "spec/fixtures/**", "scripts/ci/hub-image.sh", ".github/workflows/release.yml", ".github/workflows/hub-image.yml"],
}
SHARED = [".github/workflows/ci.yml", "scripts/ci/common.sh", "scripts/ci/run.py", "scripts/ci/paths.py", "scripts/ci/test_ci.py"]


def select(paths, all_platforms=False):
    shared = all_platforms or any(fnmatch.fnmatchcase(path, pattern) for path in paths for pattern in SHARED)
    return {platform: shared or any(fnmatch.fnmatchcase(path, pattern) for path in paths for pattern in patterns)
            for platform, patterns in FILTERS.items()}


LANES = tuple(name for name in FILTERS if name != "image")


def head_parents():
    """HEAD's parents, first parent first."""
    return subprocess.check_output(["git", "rev-parse", "HEAD^@"], timeout=30).decode().split()


def resolve_base(base=None, event=None, parents=()):
    if base:
        return base
    event = event or {}
    if "pull_request" in event:
        pull = event["pull_request"]
        # actions/checkout checks out refs/pull/<n>/merge: first parent the base branch as it is now, second the PR
        # head. base.sha is where the PR was opened and does not move as the base branch advances; comparing with it
        # would count every commit that landed since as the PR's.
        head = pull.get("head", {}).get("sha")
        if head and len(parents) == 2 and parents[1] == head:
            return parents[0]
        return pull["base"]["sha"]
    if "merge_group" in event:
        return event["merge_group"]["base_sha"]
    return "origin/main"


def select_lanes(paths, lanes="auto", all_platforms=False):
    if all_platforms or lanes == "all":
        return select([], True)
    if lanes == "auto":
        return select(paths)
    requested = {name.strip() for name in lanes.split(",")}
    unknown = requested - set(LANES)
    if unknown:
        raise ValueError(f"Unknown lanes: {', '.join(sorted(unknown))}; use auto, all, or {','.join(LANES)}")
    return {name: name in requested for name in FILTERS}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base")
    parser.add_argument("--all", action="store_true")
    parser.add_argument("--lanes", default="auto", help="auto, all, or comma-separated " + ",".join(LANES))
    parser.add_argument("--print-base", action="store_true")
    args = parser.parse_args()
    event_path = os.environ.get("GITHUB_EVENT_PATH")
    event = json.loads(Path(event_path).read_text()) if event_path else {}
    parents = head_parents() if not args.base and "pull_request" in event else ()
    base = resolve_base(args.base, event, parents)
    if args.print_base:
        # Resolve to a commit before exposing it to guards or path selection.
        sha = subprocess.check_output(["git", "rev-parse", "--verify", "--end-of-options", f"{base}^{{commit}}"], timeout=30).decode().strip()
        print(sha)
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a") as output:
                output.write(f"base={sha}\n")
        return
    paths = []
    if not args.all and args.lanes == "auto":
        raw = subprocess.check_output(["git", "diff", "--name-only", "--no-renames", "-z", f"{base}...HEAD"], timeout=30)
        paths = raw.decode().split("\0")[:-1]
    try:
        selected = select_lanes(paths, args.lanes, args.all)
    except ValueError as error:
        parser.error(str(error))
    print(json.dumps(selected, sort_keys=True))
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            for platform, needed in selected.items():
                output.write(f"{platform}={str(needed).lower()}\n")


if __name__ == "__main__":
    main()
