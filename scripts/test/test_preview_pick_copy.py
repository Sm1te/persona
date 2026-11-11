#!/usr/bin/env python3
"""The iOS preview (#150) picks an element with the same function the node's headless browser runs
(server/internal/node/preview_pick.js): the app bundles a copy (apple/Persona/Resources/Render/preview_pick.js), and
an element comment from either says the same about the element. This keeps the two copies the same."""
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]


class PreviewPickCopyTest(unittest.TestCase):
    def test_the_apps_copy_is_the_nodes(self):
        node = ROOT / "server/internal/node/preview_pick.js"
        app = ROOT / "apple/Persona/Resources/Render/preview_pick.js"
        self.assertEqual(node.read_bytes(), app.read_bytes(),
                         "apple/Persona/Resources/Render/preview_pick.js differs from server/internal/node/preview_pick.js: copy it over")


if __name__ == "__main__":
    unittest.main()
