#!/usr/bin/env python3
"""Measure the post-experiment runtime separately; do not overwrite first evidence."""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import statistics
import subprocess
import sys
import time
from run import percentile, score


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--repo',type=Path,required=True)
    p.add_argument('--output',type=Path,required=True)
    a=p.parse_args();base=Path(__file__).parent
    if a.output.exists():raise SystemExit('Refusing overwrite')
    tool=a.repo/'tools/navigation/nav.py';before=hashlib.sha256(tool.read_bytes()).hexdigest()
    tasks=json.loads((base/'tasks.json').read_text())['tasks'];rows=[]
    for repeat in range(3):
        for task in tasks[repeat:]+tasks[:repeat]:
            modes=['lexical','hybrid'] if repeat%2==0 else ['hybrid','lexical']
            for mode in modes:
                cmd=[sys.executable,str(tool),'--root',str(a.repo),'query',task['query'],'--mode',mode,'--limit','10','--max-bytes','9600']
                start=time.perf_counter();p=subprocess.run(cmd,capture_output=True);elapsed=time.perf_counter()-start
                obj=json.loads(p.stdout);paths=[r['path'] for r in obj.get('results',[])]
                rows.append({'id':task['id'],'split':task['split'],'mode':mode,'repeat':repeat,'exit':p.returncode,
                             'seconds':elapsed,'freshness_seconds':obj.get('freshness_seconds'),
                             'retrieval_seconds':obj.get('retrieval_seconds'),'paths':paths,'output_bytes':len(p.stdout),
                             'metrics':score(paths,task),'input_fingerprint':obj.get('input_fingerprint')})
    text=[]
    for task in tasks:
        cmd=[sys.executable,str(tool),'--root',str(a.repo),'locate',task['query'],'--mode','lexical','--limit','10','--max-bytes','9600']
        start=time.perf_counter();p=subprocess.run(cmd,capture_output=True)
        # Keep only bytes/hash: source snippets remain in the private runtime output.
        text.append({'id':task['id'],'exit':p.returncode,'seconds':time.perf_counter()-start,
                     'output_bytes':len(p.stdout),'output_sha256':hashlib.sha256(p.stdout).hexdigest()})
    after=hashlib.sha256(tool.read_bytes()).hexdigest()
    assert before==after,'Runtime changed during measurement'
    assert not [r for r in rows+text if r['exit']]
    assert len({r['input_fingerprint'] for r in rows})==1
    summary={}
    for mode in ('lexical','hybrid'):
        rs=[r for r in rows if r['mode']==mode]
        test=[r for r in rs if r['repeat']==0 and r['split']=='test' and r['metrics']['all']]
        summary[mode]={'queries':len(rs),'p50_seconds':percentile([r['seconds'] for r in rs],.5),
                       'p95_seconds':percentile([r['seconds'] for r in rs],.95),
                       'p50_freshness_seconds':percentile([r['freshness_seconds'] for r in rs],.5),
                       'p50_retrieval_seconds':percentile([r['retrieval_seconds'] for r in rs],.5),
                       'test_judged_hit@10':statistics.mean(r['metrics']['all']['hit@10'] for r in test),
                       'mean_json_bytes':statistics.mean(r['output_bytes'] for r in rs)}
    summary['text_lexical']={'queries':len(text),'mean_bytes':statistics.mean(r['output_bytes'] for r in text),
                             'p50_seconds':percentile([r['seconds'] for r in text],.5)}
    result={'phase':'Post-experiment runtime validation; not a fresh heldout algorithm trial.',
            'runtime_sha256':before,'python':platform.python_version(),'head':subprocess.check_output(['git','-C',str(a.repo),'rev-parse','HEAD']).decode().strip(),
            'limits':['Two tracked navigation-document integration edits distinguish this corpus from frozen first_run.',
                      'Same questions and ranking algorithms, no question-specific tuning; results are exploratory regression/performance evidence.',
                      'Warm filesystem/process queries include interpreter start and normal stat freshness, not adversarial integrity checks.',
                      'Concurrent pilot agents can contend for filesystem/model resources; no isolated CPU benchmark claimed.'],
            'summary':summary,'queries':rows,'text_output_measurements':text}
    a.output.write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(summary,indent=2))

if __name__=='__main__':main()
