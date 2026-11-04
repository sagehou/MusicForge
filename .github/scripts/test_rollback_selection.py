"""Keep daily rollback bounded without weakening the published baseline."""

import importlib.util
from pathlib import Path
import unittest
from unittest.mock import Mock, call

spec = importlib.util.spec_from_file_location("rollback", Path(__file__).with_name("check-rollback.py"))
rollback = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rollback)


class RollbackSelectionTests(unittest.TestCase):
    def test_daily_run_uses_only_the_published_predecessor(self):
        self.assertEqual(rollback.selected_releases("v0.2.49"), {"v0.2.49": rollback.LEGACY["v0.2.49"]})
        self.assertEqual(rollback.selected_releases("v0.2.99"), {"v0.2.99": None})

    def test_historical_run_retains_pins_and_excludes_the_candidate(self):
        selected = rollback.selected_releases("v0.2.48", True, "v0.2.49")
        self.assertNotIn("v0.2.49", selected)
        self.assertEqual(selected["v0.2.0"], rollback.LEGACY["v0.2.0"])
        self.assertEqual(selected["v0.2.48"], rollback.LEGACY["v0.2.48"])

    def test_missing_or_unrelated_baselines_fail_closed(self):
        for baseline in (None, "", "v0.1.0", "v0.3.0", "v0.2.50-rc1"):
            with self.assertRaises(ValueError):
                rollback.selected_releases(baseline)
        with self.assertRaises(ValueError):
            rollback.selected_releases("v0.2.49", current_tag="v0.2.49")

    def test_lyrics_option_writes_preserve_the_release_supported_order(self):
        providers = ["amll", "petitlyrics", "unison", "lrclib", "migu"]
        for version, expected in (("v0.2.58", ["petitlyrics", "lrclib", "migu"]),
                                  ("v0.2.59", providers), ("v0.2.99", providers)):
            with self.subTest(version=version):
                api = Mock(side_effect=[{"options": {"providers": expected}}, {"providers": expected}, {}, {}])
                rollback.verify_lyrics_options(api, version, providers)
                self.assertEqual(api.call_args_list, [call("/api/lyrics"),
                    call("/api/lyrics/options", "PUT", {"providers": expected}),
                    call("/api/lyrics/options", "PUT", {"auto_search": False}),
                    call("/api/lyrics/search", "DELETE")])
        api = Mock()
        rollback.verify_lyrics_options(api, "v0.2.30", providers)
        api.assert_not_called()
        api = Mock(side_effect=[{"options": {"auto_search": False}}, {}, {}])
        rollback.verify_lyrics_options(api, "v0.2.31", providers)
        self.assertEqual(api.call_args_list, [call("/api/lyrics"),
            call("/api/lyrics/options", "PUT", {"auto_search": False}), call("/api/lyrics/search", "DELETE")])

    def test_lyrics_provider_loss_or_leak_is_rejected(self):
        providers = ["amll", "lrclib", "unison"]
        for version, wrong in (("v0.2.58", providers), ("v0.2.59", ["lrclib"])):
            api = Mock(return_value={"options": {"providers": wrong}})
            with self.subTest(version=version), self.assertRaises(AssertionError):
                rollback.verify_lyrics_options(api, version, providers)
            api.assert_called_once_with("/api/lyrics")
        api = Mock(side_effect=[{"options": {"providers": providers}}, {"providers": ["lrclib"]}])
        with self.assertRaises(AssertionError):
            rollback.verify_lyrics_options(api, "v0.2.59", providers)
