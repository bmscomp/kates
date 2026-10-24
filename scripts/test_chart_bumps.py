#!/usr/bin/env python3
"""Tests for scripts/chart_bumps.py.

Run: python3 -m unittest discover -s scripts -p 'test_chart_bumps.py'

The rule tests build a throwaway git repository with a chart and an umbrella
that packs it, commit that as the base, and change the working tree on top.
"""

import os
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import chart_bumps as cb  # noqa: E402

HELMIGNORE = "*.swp\n.DS_Store\nvalues-*.yaml\n/tests/\nci/\n"


class HelmignoreTest(unittest.TestCase):
    rules = cb.parse_helmignore(HELMIGNORE)

    def test_basename_rules_match_at_any_depth(self):
        self.assertTrue(cb.ignored("values-kind.yaml", self.rules))
        self.assertTrue(cb.ignored("templates/notes.swp", self.rules))
        self.assertFalse(cb.ignored("values.yaml", self.rules))

    def test_anchored_directory_rule_matches_the_root_only(self):
        self.assertTrue(cb.ignored("tests/policy_test.yaml", self.rules))
        self.assertFalse(cb.ignored("templates/tests/test-pod.yaml", self.rules))

    def test_unanchored_directory_rule_matches_at_any_depth(self):
        self.assertTrue(cb.ignored("ci/values.yaml", self.rules))
        self.assertTrue(cb.ignored("files/ci/x.json", self.rules))

    def test_a_directory_rule_does_not_match_a_file(self):
        self.assertFalse(cb.ignored("ci", self.rules))

    def test_a_star_does_not_cross_a_slash(self):
        rules = cb.parse_helmignore("templates/*.yaml\n")
        self.assertTrue(cb.ignored("templates/a.yaml", rules))
        self.assertFalse(cb.ignored("templates/sub/a.yaml", rules))

    def test_negation_is_refused(self):
        with self.assertRaises(ValueError):
            cb.parse_helmignore("!keep.yaml\n")


class ChartYamlTest(unittest.TestCase):
    chart = textwrap.dedent(
        """\
        name: umbrella
        version: 0.8.3
        dependencies:
          - name: a
            version: ">=0.1.0"
            repository: "file://../a"
          - name: seaweedfs
            version: 3.68.0
            repository: https://seaweedfs.github.io/seaweedfs/helm
        """
    )

    def test_the_top_level_version_not_a_dependency_s(self):
        self.assertEqual(cb.chart_version(self.chart), "0.8.3")

    def test_only_file_dependencies_are_listed(self):
        self.assertEqual(cb.file_dependencies(self.chart), ["a"])

    def test_versions_order_numerically_and_pre_releases_first(self):
        self.assertLess(cb.semver_key("1.0.9"), cb.semver_key("1.0.10"))
        self.assertLess(cb.semver_key("1.0.0-rc.1"), cb.semver_key("1.0.0"))


class RulesTest(unittest.TestCase):
    def setUp(self):
        self.cwd = os.getcwd()
        self.dir = tempfile.TemporaryDirectory()
        os.chdir(self.dir.name)
        self.git("init", "-q")
        self.write("charts/a/Chart.yaml", "name: a\nversion: 0.1.0\n")
        self.write("charts/a/.helmignore", HELMIGNORE)
        self.write("charts/a/templates/cm.yaml", "kind: ConfigMap\n")
        self.write("charts/a/tests/cm_test.yaml", "suite: cm\n")
        self.write("charts/a/README.md", "# a\n")
        self.write("charts/umbrella/Chart.yaml", ChartYamlTest.chart)
        self.git("add", "-A")
        self.git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "base")

    def tearDown(self):
        os.chdir(self.cwd)
        self.dir.cleanup()

    def git(self, *args):
        subprocess.run(["git", *args], check=True, capture_output=True)

    def write(self, path, text):
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        Path(path).write_text(text)

    def problems(self):
        return cb.problems("HEAD")

    def test_nothing_changed_passes(self):
        self.assertEqual(self.problems(), [])

    def test_a_template_change_without_a_bump_fails(self):
        self.write("charts/a/templates/cm.yaml", "kind: ConfigMap\ndata: {}\n")
        [problem] = self.problems()
        self.assertIn("a: templates/cm.yaml changed", problem)

    def test_ignored_and_docs_changes_need_no_bump(self):
        self.write("charts/a/tests/cm_test.yaml", "suite: cm2\n")
        self.write("charts/a/values-kind.yaml", "x: 1\n")
        self.write("charts/a/README.md", "# a, reworded\n")
        self.assertEqual(self.problems(), [])

    def test_a_bumped_subchart_needs_a_bumped_umbrella(self):
        self.write("charts/a/Chart.yaml", "name: a\nversion: 0.1.1\n")
        [problem] = self.problems()
        self.assertIn("umbrella: it packs a, which moved from 0.1.0 to 0.1.1", problem)

    def test_both_bumped_passes(self):
        self.write("charts/a/Chart.yaml", "name: a\nversion: 0.1.1\n")
        self.write("charts/umbrella/Chart.yaml", ChartYamlTest.chart.replace("0.8.3", "0.8.4"))
        self.assertEqual(self.problems(), [])

    def test_a_version_that_goes_down_fails(self):
        self.write("charts/a/Chart.yaml", "name: a\nversion: 0.0.9\n")
        self.write("charts/umbrella/Chart.yaml", ChartYamlTest.chart.replace("0.8.3", "0.8.4"))
        [problem] = self.problems()
        self.assertIn("a: the version went from 0.1.0 to 0.0.9", problem)

    def test_a_new_chart_needs_no_bump(self):
        self.write("charts/b/Chart.yaml", "name: b\nversion: 0.1.0\n")
        self.git("add", "charts/b")
        self.assertEqual(self.problems(), [])


if __name__ == "__main__":
    unittest.main()
