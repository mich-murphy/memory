#!/usr/bin/env python3
"""Join condition-blinded supplemental relevance judgments to pilot runs.

Does not change frozen qrels or original scores. No unknown path is treated as
irrelevant. This is post-outcome supplemental adjudication, not a heldout rerun.
"""
import argparse
import hashlib
import json
from pathlib import Path


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root',type=Path,default=Path('/Users/mm/businesscraft/businesscraft'))
    p.add_argument('--pilot',type=Path,default=Path(__file__).with_name('pilot-results.json'))
    p.add_argument('--output',type=Path,required=True)
    p.add_argument('--extra-review',type=Path,action='append',default=[])
    p.add_argument('--extra-input',type=Path,action='append',default=[])
    a=p.parse_args(); base=Path(__file__).parent
    if a.output.exists():raise SystemExit('Refusing overwrite')
    judged=json.loads((base/'blind-path-review.json').read_text())
    relevance={(j['task_id'],j['path']):j for j in judged['judgments']}
    expected=json.loads((base/'blind-path-review-input.json').read_text())['items']
    expected_pairs={(x['id'],path) for x in expected for path in x['candidate_paths']}
    if len(a.extra_review)!=len(a.extra_input):raise ValueError('Pair extra inputs and reviews')
    for review,input_path in zip(a.extra_review,a.extra_input):
        additional=json.loads(review.read_text())['judgments']
        input_pairs={(x['id'],path) for x in json.loads(input_path.read_text())['items'] for path in x['candidate_paths']}
        actual={(j['task_id'],j['path']) for j in additional}
        if actual!=input_pairs or actual & set(relevance):raise ValueError('Incomplete or overlapping supplemental review')
        relevance.update({(j['task_id'],j['path']):j for j in additional})
        expected_pairs |= input_pairs
    if set(relevance)!=expected_pairs:raise ValueError('Incomplete blinded review')
    for (task,path),j in relevance.items():
        raw=(a.root/path).read_bytes()
        if hashlib.sha256(raw).hexdigest()!=j['evidence']['sha256']:
            raise ValueError('Judgment source changed')
        if not 1<=j['evidence']['line_start']<=j['evidence']['line_end']<=len(raw.splitlines()):
            raise ValueError('Invalid citation lines')
    runs=json.loads(a.pilot.read_text())['runs']; result={}; details=[]
    for run in runs:
        for answer in run['answer']['answers']:
            if answer['id']=='bcnav-18':continue
            grades=[relevance.get((answer['id'],path),{}).get('grade') for path in answer['paths']]
            rank=next((i+1 for i,g in enumerate(grades) if g is not None and g>=2),None)
            details.append({'condition':run['answer']['condition'],'bundle':run['answer']['bundle'],
                            'task_id':answer['id'],'grades':grades,'useful_entrypoint_hit@5':int(rank is not None),
                            'mrr':1/rank if rank else 0,'unknown_paths':sum(g is None for g in grades)})
    for c in sorted({x['condition'] for x in details}):
        rows=[x for x in details if x['condition']==c]
        result[c]={'judged_tasks':len(rows),'useful_entrypoint_hit@5':sum(x['useful_entrypoint_hit@5'] for x in rows)/len(rows),
                   'mrr':sum(x['mrr'] for x in rows)/len(rows),'unknown_paths':sum(x['unknown_paths'] for x in rows)}
    out={'policy':f'Supplemental blind review of all {len(relevance)} unique returned task/path pairs; condition identity removed before adjudication.',
         'source_checks':'All cited file hashes and line ranges verified.',
         'limits':['Original frozen qrels and pilot scores remain unchanged.',
                   'Broad useful-entrypoint relevance, not full task correctness, sufficient facet coverage, or downstream coding quality.',
                   'Post-outcome adjudication is not independent held-out confirmation or a significance test.'],
         'summary':result,'details':details,
         'review_sha256':hashlib.sha256((base/'blind-path-review.json').read_bytes()).hexdigest(),
         'extra_review_sha256':[hashlib.sha256(x.read_bytes()).hexdigest() for x in a.extra_review]}
    a.output.write_text(json.dumps(out,indent=2)+'\n');print(json.dumps(result,indent=2))

if __name__=='__main__':main()
