#!/usr/bin/env python3
"""Compare complete collection behavior on paired baseline/candidate runs."""
import argparse
import json
import os
from pathlib import Path
import re
import statistics
import subprocess

BENCH = re.compile(r'^BenchmarkCollection/filtered=(true|false)-\d+\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op$', re.MULTILINE)
HEAP = re.compile(r'collection retained heap delta: (-?\d+) bytes')


def run(directory, output, arguments):
    environment = dict(os.environ, GOWORK='off', GOMAXPROCS='2', GOMEMLIMIT='160MiB', CLOUDFLARE_INTEGRATION='')
    result = subprocess.run(['go', 'test', '-p', '1', './internal/cloudflare', *arguments], cwd=directory,
                            env=environment, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            timeout=180)
    output.write_text(result.stdout)
    if result.returncode:
        raise SystemExit(f'Collection measurement failed; see {output}')
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--baseline', type=Path, required=True)
    parser.add_argument('--candidate', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    samples = {name: {'false': [], 'true': [], 'heap': []} for name in ['baseline', 'candidate']}
    for pair in range(5):
        # Alternate execution order to reduce persistent warm-up/order bias.
        for name in (['baseline', 'candidate'] if pair % 2 == 0 else ['candidate', 'baseline']):
            directory = getattr(args, name)
            text = run(directory, args.output/f'{pair}-{name}-collection.txt',
                       ['-run', '^$', '-bench', '^BenchmarkCollection/', '-benchmem', '-benchtime=5x', '-count=1'])
            found = list(BENCH.finditer(text))
            if len(found) != 2 or {m[1] for m in found} != {'false', 'true'}:
                raise SystemExit('Missing or duplicate collection benchmark scenarios')
            for match in found:
                samples[name][match[1]].append({'ns': float(match[2]), 'bytes': int(match[3]), 'allocs': int(match[4])})
            text = run(directory, args.output/f'{pair}-{name}-heap.txt',
                       ['-run', '^TestCollectionRetainedHeap$', '-v', '-count=1'])
            found = HEAP.findall(text)
            if len(found) != 1 or int(found[0]) <= 0:
                raise SystemExit('Invalid retained heap measurement')
            samples[name]['heap'].append(int(found[0]))
    ratios = {}
    for scenario in ['false', 'true']:
        ratios[scenario] = {}
        for metric in ['ns', 'bytes', 'allocs']:
            base = statistics.median(x[metric] for x in samples['baseline'][scenario])
            candidate = statistics.median(x[metric] for x in samples['candidate'][scenario])
            if base <= 0:
                raise SystemExit('Nonpositive collection baseline')
            ratios[scenario][metric] = candidate/base
    ratios['heap'] = statistics.median(samples['candidate']['heap'])/statistics.median(samples['baseline']['heap'])
    passed = all(ratios[s]['bytes'] <= .70 and ratios[s]['ns'] <= 1.05 for s in ['false', 'true']) and ratios['heap'] <= 1.10
    commits = {name: subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=getattr(args, name), text=True).strip()
               for name in samples}
    report = {'commits': commits, 'pairs': 5, 'operations_per_sample': 5, 'samples': samples,
              'ratios': ratios, 'passed': passed,
              'limits': {'bytes': .70, 'ns': 1.05, 'filtered_retained_heap': 1.10}}
    (args.output/'comparison.json').write_text(json.dumps(report, indent=2)+'\n')
    print(json.dumps({'commits': commits, 'ratios': ratios, 'passed': passed}, indent=2))
    if not passed:
        raise SystemExit('Collection acceptance thresholds not met')


if __name__ == '__main__':
    main()
