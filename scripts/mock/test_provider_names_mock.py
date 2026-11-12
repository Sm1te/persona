"""mock_hub.py names a custom provider and carries display names as the hub does (#5029, launcher_auto_name,
docs/integrations/launchers.md "Display names"; the hub's own cases are in
server/internal/assistant/models_launchers_names_test.go). Each case runs its own mock hub on a free port."""
import unittest

from test_readiness_mock import hub


def added(test, h, body):
    status, out = h.call("POST", "/api/v1/launchers", dict(body, model="m"))
    test.assertEqual(status, 200, out)
    return out["launcher"]["name"], out["launcher"].get("display_name", "")


class ProviderNamesMockTests(unittest.TestCase):
    def test_providers_name_a_custom_gateway(self):
        h = hub(self, MOCK_PROVIDERS="1")
        _, health = h.call("GET", "/api/v1/health")
        self.assertIn("launcher_auto_name", health["features"])
        for body, want in (
            ({"display_name": "我的中转 站", "base_url": "https://relay.example.com"}, ("relay-example-com", "我的中转 站")),
            ({"display_name": "Team Relay", "base_url": "https://gw.example.com"}, ("team-relay", "Team Relay")),
            ({"name": "", "display_name": "Team Relay", "base_url": "https://gw.example.com"}, ("team-relay-2", "Team Relay")),
            ({"display_name": "", "base_url": "https://gw.example.com/v1"}, ("gw-example-com", "gw.example.com")),
            ({"base_url": "https://GW.example.com:8443"}, ("gw-example-com-2", "gw.example.com 2")),
            ({"display_name": "Test", "base_url": "https://gw.example.com"}, ("test-2", "Test")),
            ({"display_name": "1min relay", "base_url": "https://gw.example.com"}, ("p-1min-relay", "1min relay")),
            ({"display_name": "A very long name for the gateway our whole team shares", "base_url": "https://gw.example.com"},
             ("a-very-long-name-for-the-gateway-our-who", "A very long name for the gateway our whole team shares")),
        ):
            with self.subTest(body=body):
                self.assertEqual(added(self, h, body), want)
        status, _ = h.call("POST", "/api/v1/launchers", {"display_name": "a\nb", "base_url": "https://gw.example.com", "model": "m"})
        self.assertEqual(status, 400)

    def test_put_renames_and_keeps(self):
        h = hub(self, MOCK_PROVIDERS="1")
        for body, want in (({"display_name": " 团队网关 "}, "团队网关"), ({"model": "example-sonnet"}, "团队网关"), ({"display_name": ""}, None)):
            status, out = h.call("PUT", "/api/v1/launchers/gw_full", body)
            self.assertEqual((status, out["launcher"].get("display_name")), (200, want), body)
        status, _ = h.call("PUT", "/api/v1/launchers/gw_full", {"display_name": "x" * 65})
        self.assertEqual(status, 400)

    def test_new_agent_lists_managed_providers_with_display_names(self):
        _, plain = hub(self).call("GET", "/api/v1/machines/macbook/launchers")
        self.assertEqual([l["name"] for l in plain["launchers"]], ["claude", "sonnetcc", "codex"])
        h = hub(self, MOCK_PROVIDERS="1")
        added(self, h, {"display_name": "我的中转", "base_url": "https://relay.example.com"})
        _, out = h.call("GET", "/api/v1/machines/macbook/launchers")
        shown = {l["name"]: l for l in out["launchers"]}
        self.assertEqual(shown["gw_full"]["display_name"], "Team gateway")
        self.assertEqual(shown["gw_full"]["source"], "persona")
        self.assertNotIn("display_name", shown["anthropic_main"])
        self.assertEqual(shown["relay-example-com"]["display_name"], "我的中转")
        self.assertNotIn("display_name", shown["claude"])

    def test_builtin_presets_hub_names_a_custom_gateway_too(self):
        h = hub(self, MOCK_BUILTIN_PRESETS="1")
        _, health = h.call("GET", "/api/v1/health")
        self.assertIn("launcher_auto_name", health["features"])
        self.assertEqual(added(self, h, {"base_url": "https://gw.example.com"}), ("gw-example-com", "gw.example.com"))
        status, out = h.call("POST", "/api/v1/launchers", {"preset": "builtin/glm", "api_key": "sk-test"})
        self.assertEqual((status, out["launcher"]["name"]), (200, "glm"))
        self.assertNotIn("launcher_auto_name", hub(self).call("GET", "/api/v1/health")[1]["features"])


if __name__ == "__main__":
    unittest.main()
