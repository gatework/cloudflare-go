import contextlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import compare_collection


class CollectionComparisonTest(unittest.TestCase):
    def compare(self, directory, *, regression=False, missing=False, heap=100):
        calls = []

        def measure(command, *, cwd, env, **kwargs):
            name = cwd.name
            calls.append((name, command))
            self.assertEqual(env['GOMAXPROCS'], '2')
            self.assertEqual(env['GOMEMLIMIT'], '160MiB')
            self.assertEqual(env['CLOUDFLARE_INTEGRATION'], '')
            if '-bench' in command:
                self.assertIn('-benchtime=5x', command)
                # Four ordinary samples and an outlier: the gate uses medians.
                occurrence = sum(n == name and '-bench' in c for n, c in calls)
                ns, allocation = 1000, 1000
                if name == 'candidate':
                    ns = 2000 if occurrence == 1 else 1000
                    allocation = 701 if regression else 700
                text = f'BenchmarkCollection/filtered=false-2 5 {ns} ns/op {allocation} B/op 100 allocs/op\n'
                if not missing:
                    text += f'BenchmarkCollection/filtered=true-2 5 {ns} ns/op {allocation} B/op 100 allocs/op\n'
            else:
                retained = 100 if name == 'baseline' else heap
                text = f'collection retained heap delta: {retained} bytes\n'
            return subprocess.CompletedProcess(command, 0, stdout=text)

        args = ['compare_collection.py', '--baseline', '/fixture/baseline',
                '--candidate', '/fixture/candidate', '--output', directory]
        with patch.object(sys, 'argv', args), \
                patch.object(compare_collection.subprocess, 'run', side_effect=measure), \
                patch.object(compare_collection.subprocess, 'check_output', return_value='a'*40+'\n'), \
                contextlib.redirect_stdout(io.StringIO()):
            compare_collection.main()
        return json.loads((Path(directory)/'comparison.json').read_text()), calls

    def test_medians_threshold_boundaries_and_alternating_pairs(self):
        with tempfile.TemporaryDirectory() as directory:
            report, calls = self.compare(directory, heap=110)
        self.assertTrue(report['passed'])
        self.assertEqual(report['ratios']['true']['ns'], 1)
        self.assertEqual(report['ratios']['false']['bytes'], .7)
        self.assertEqual(report['ratios']['heap'], 1.1)
        self.assertEqual([name for name, cmd in calls if '-bench' in cmd],
                         ['baseline', 'candidate', 'candidate', 'baseline',
                          'baseline', 'candidate', 'candidate', 'baseline',
                          'baseline', 'candidate'])

    def test_allocation_regression_fails_and_preserves_report(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(SystemExit, 'thresholds not met'):
                self.compare(directory, regression=True)
            report = json.loads((Path(directory)/'comparison.json').read_text())
        self.assertFalse(report['passed'])

    def test_missing_scenario_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(SystemExit, 'Missing or duplicate'):
                self.compare(directory, missing=True)

    def test_invalid_heap_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(SystemExit, 'Invalid retained heap'):
                self.compare(directory, heap=0)


if __name__ == '__main__':
    unittest.main()
