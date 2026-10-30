#!/usr/bin/env python3
"""Summarize NEW experimental agent runs; never used to mine task history.

Raw session transcripts stay private. Registry filenames identify only experimental
runs. No tool arguments/results or prompt text are copied into the proof report.
"""
import argparse
import collections
import datetime
import hashlib
import json
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).parent))
from run import score


def parse(path):
    data = path.read_bytes()
    records = [json.loads(line) for line in data.splitlines() if line.strip()]
    usage = collections.Counter()
    tools = collections.Counter()
    output_bytes = 0
    output_sizes = []
    last_text = ''
    models = set()
    times = []
    for record in records:
        if record.get('timestamp'):
            try:
                times.append(datetime.datetime.fromisoformat(record['timestamp'].replace('Z','+00:00')))
            except (ValueError,TypeError):
                pass
        message = record.get('message', {})
        content = message.get('content', [])
        if isinstance(content, str):
            content = [{'type':'text','text':content}]
        if message.get('role') == 'assistant':
            models.add((message.get('provider'),message.get('model'),message.get('thinkingLevel')))
            for name in ('input','output','cacheRead','cacheWrite','reasoning','totalTokens'):
                value = message.get('usage',{}).get(name)
                if isinstance(value,(int,float)):
                    usage[name] += value
            for block in content:
                if isinstance(block,dict) and block.get('type') == 'toolCall':
                    tools[block.get('name','unknown')] += 1
            text = '\n'.join(b.get('text','') for b in content if isinstance(b,dict) and b.get('type')=='text')
            if text.strip():
                last_text = text
        if message.get('role') in ('toolResult','tool'):
            size = sum(len(b.get('text','').encode()) for b in content if isinstance(b,dict) and b.get('type')=='text')
            output_bytes += size
            output_sizes.append(size)
    clean = last_text.strip()
    if clean.startswith('```'):
        clean = clean.split('\n',1)[1].rsplit('```',1)[0]
    answer = json.loads(clean)
    return {'source_sha256':hashlib.sha256(data).hexdigest(),
            'recorded_wall_seconds':(max(times)-min(times)).total_seconds() if times else None,
            'model_observations':[list(x) for x in sorted(models,key=str)],
            'usage_sum':dict(usage),'tool_calls':sum(tools.values()),'tool_names':dict(tools),
            'tool_result_utf8_bytes':output_bytes,'largest_tool_result_bytes':max(output_sizes,default=0),
            'answer':answer}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--sessions',type=Path,required=True)
    p.add_argument('--registry',type=Path,default=Path(__file__).with_name('pilot-registry.json'))
    p.add_argument('--tasks',type=Path,default=Path(__file__).with_name('tasks.json'))
    p.add_argument('--output',type=Path,required=True)
    a = p.parse_args()
    if a.output.exists():
        raise SystemExit('Refusing overwrite; preserve first evidence')
    tasks = {t['id']:t for t in json.loads(a.tasks.read_text())['tasks']}
    registry = json.loads(a.registry.read_text())
    rows = []
    for entry in registry['runs']:
        source = a.sessions / entry['filename']
        row = parse(source)
        row.update(agent_id=entry['agent_id'],filename=entry['filename'])
        answer = row['answer']
        if answer['condition'] != entry['condition'] or answer['bundle'] != entry['bundle']:
            raise ValueError('Condition identity mismatch')
        if len(answer['answers']) != 6:
            raise ValueError('Expected six tasks per run')
        metrics = []
        for item in answer['answers']:
            task = tasks[item['id']]
            if len(item['paths']) > 5:
                raise ValueError('Five-file answer cap exceeded')
            item['metrics'] = score(item['paths'], task)
            item['external_abstention_correct'] = (bool(item.get('abstain')) and not item['paths']) if task['answerability'] != 'repository_navigation' else None
            item['existing_paths'] = all((Path(registry['repo'])/path).is_file() for path in item['paths'])
            metrics.append(item['metrics'])
        rows.append(row)
    summary = {}
    for condition in sorted({r['answer']['condition'] for r in rows}):
        runs = [r for r in rows if r['answer']['condition']==condition]
        answers = [x for r in runs for x in r['answer']['answers']]
        valid = [x for x in answers if x['metrics']['all'] is not None]
        summary[condition] = {'runs':len(runs),'tasks':len(answers),'judged_tasks':len(valid),
                              'judged_hit@5':sum(x['metrics']['all']['hit@5'] for x in valid)/len(valid),
                              'judged_mrr':sum(x['metrics']['all']['mrr'] for x in valid)/len(valid),
                              'judged_group_coverage@5':sum(x['metrics']['all']['graded_group_coverage@5'] for x in valid)/len(valid),
                              'tool_calls':sum(r['tool_calls'] for r in runs),
                              'tool_result_utf8_bytes':sum(r['tool_result_utf8_bytes'] for r in runs),
                              'reported_input_tokens':sum(r['usage_sum'].get('input',0) for r in runs),
                              'reported_cache_read_tokens':sum(r['usage_sum'].get('cacheRead',0) for r in runs),
                              'reported_output_tokens':sum(r['usage_sum'].get('output',0) for r in runs),
                              'recorded_wall_seconds_sum':sum(r['recorded_wall_seconds'] or 0 for r in runs),
                              'all_returned_paths_exist':all(x['existing_paths'] for x in answers)}
    result = {'protocol':f'{len(rows)} fresh same-model medium-effort agents; two bundles of six tasks per condition; 18 tool-call budget per run. Initial task split was held out; follow-up runs reuse those tasks and are exploratory.',
              'limits':['One run per condition/bundle, not a powered statistical experiment.',
                        'Qrels incomplete: unjudged answers remain unknown; scores are judged entrypoint metrics, not full correctness.',
                        'Tool-result UTF-8 bytes are actual observed text; request usage includes repeated prompt prefixes and is not unique context.',
                        'Provider-reported token fields retained separately; no dollar savings inferred.',
                        'Source transcripts are NEW experiment logs, not the source of historical tasks; tasks come from verified R2 archive.',
                        'Wall times include model reasoning/queue-independent experiment processing and do not isolate retrieval latency.'],
              'summary':summary,'runs':rows}
    a.output.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(summary,indent=2))

if __name__=='__main__':
    main()
