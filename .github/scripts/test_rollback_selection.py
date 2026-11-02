"""Keep daily rollback bounded without weakening the published baseline."""

import importlib.util
from pathlib import Path
import unittest

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
