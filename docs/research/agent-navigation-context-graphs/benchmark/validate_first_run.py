#!/usr/bin/env python3
"""Post-run consistency checks; never changes the frozen benchmark or judgments."""
import argparse
import collections
import hashlib
import gzip
import importlib.util
import json
from pathlib import Path
import subprocess
import sys


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--repo',type=Path,required=True)
    p.add_argument('--output',type=Path,required=True)
    a=p.parse_args();base=Path(__file__).parent;run=base/'first_run'
    if a.output.exists():raise SystemExit('Refusing overwrite')
    subprocess.run([sys.executable,str(base/'validate_dataset.py'),'--repo',str(a.repo)],check=True,capture_output=True)
    raw = (run/'queries.jsonl').read_bytes() if (run/'queries.jsonl').exists() else gzip.decompress((run/'queries.jsonl.gz').read_bytes())
    rows=[json.loads(x) for x in raw.decode().splitlines()]
    assert len(rows)==400
    assert not [r for r in rows if 'error' in r]
    by_key=collections.defaultdict(list)
    for r in rows:
        assert len(r['output'].encode())==r['output_bytes']<=9600
        if r.get('native_output'):
            assert len(r['native_output'].encode())<=9600
        by_key[(r['task_id'],r['variant'])].append(r)
    assert len(by_key)==100
    for group in by_key.values():
        assert sorted(r['repeat'] for r in group)==[0,1,2,3]
        assert len({tuple(x['path'] for x in r['results']) for r in group})==1
    build=json.loads((run/'build.stdout').read_text())
    proto=base.parent/'prototype'
    spec=importlib.util.spec_from_file_location('frozen_nav_verify',proto/'nav.py')
    nav=importlib.util.module_from_spec(spec);spec.loader.exec_module(nav)
    current=nav.stamp(a.repo.resolve(),proto/'routing.json',nav.inventory(a.repo.resolve()))
    assert all(build[k]==v for k,v in current.items()),'Target corpus differs from recorded build'
    manifest=json.loads((run/'manifest.json').read_text())
    for f in ('prototype/nav.py','prototype/routing.json','prototype/map.md','benchmark/tasks.json','benchmark/run.py','benchmark/SCORING.md'):
        assert hashlib.sha256((base.parent/f).read_bytes()).hexdigest()==manifest['hashes'][f]
    result={'status':'passed','rows':len(rows),'task_variant_pairs':len(by_key),'errors':0,
            'repeats_per_pair':4,'rankings_stable':True,'normalized_and_native_byte_caps_passed':True,
            'dataset_qrel_hashes_and_provenance_passed':True,
            'frozen_source_and_tasks_match_run_manifest':True,
            'post_run_corpus_content_fingerprint_matches_build':True,
            'corpus_content_fingerprint':current['inputs'],'head':current['head'],
            'notes':['Validated before integration edits to tracked BusinessCraft documentation.',
                     'Matching before/after fingerprints do not establish absence of undetected change-and-revert races.',
                     'Original harness weaknesses remain disclosed in review.md; zero errors means failed-query exclusion did not inflate this run.',
                     'Byte caps are upper bounds; different envelopes consume different shares of the cap.']}
    a.output.write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))

if __name__=='__main__':main()
