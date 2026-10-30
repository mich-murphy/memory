import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, chmod, readFile, stat, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { boundedMap, canonical, contentKey, DEFAULT_QUESTIONS, main, privateWrite, validateInput, validateQuestions } from './classify.mjs';

async function temporary(t) {
  const dir = await mkdtemp(join(tmpdir(), 'classify-test-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  return dir;
}
test('stable content hash includes model, questions and full state', () => {
  assert.equal(canonical({ z: 1, a: { b: 2 } }), canonical({ a: { b: 2 }, z: 1 }));
  assert.equal(canonical({ id: 'jev', cost: undefined }), canonical({ id: 'jev' }));
  const key = contentKey({ id: 'jev' }, DEFAULT_QUESTIONS, { body: 'one' });
  assert.equal(key.length, 64);
  for (const [model, questions, state] of [[{ id: 'other' }, DEFAULT_QUESTIONS, { body: 'one' }], [{ id: 'jev' }, {}, { body: 'one' }], [{ id: 'jev' }, DEFAULT_QUESTIONS, { body: 'two' }]]) assert.notEqual(key, contentKey(model, questions, state));
});
test('validates typed questions and refuses malformed/oversized input', () => {
  validateQuestions(DEFAULT_QUESTIONS);
  validateQuestions({ x: { type: 'score', instructions: 'rank', criteria: ['low', 'high'] } });
  for (const q of [{}, { x: { type: 'bool', instructions: 'x', criteria: { yes: '', no: '' } } }, { x: { type: 'text', instructions: 'x' } }]) assert.throws(() => validateQuestions(q));
  const item = { id: 'a', state: { text: 'private' } };
  validateInput([item], 1, 100);
  for (const [input, max, bytes] of [[[item, item], 2, 100], [[item], 0, 100], [[item], 1, 1], [[{ id: 'x', state: 'bad' }], 1, 100]]) assert.throws(() => validateInput(input, max, bytes));
});
test('bounded workers retain order and never exceed concurrency', async () => {
  let active = 0, peak = 0;
  const result = await boundedMap([1, 2, 3, 4, 5], 2, async n => {
    peak = Math.max(peak, ++active);
    await new Promise(r => setTimeout(r, 5));
    --active;
    return n * 2;
  });
  assert.deepEqual(result, [2, 4, 6, 8, 10]);
  assert.equal(peak, 2);
});
test('private atomic JSON writes create private directories and files', async t => {
  const dir = await temporary(t);
  const path = join(dir, 'new', 'result.json');
  await privateWrite(path, { private: 'value' });
  assert.equal((await stat(path)).mode & 0o777, 0o600);
  assert.equal((await stat(join(dir, 'new'))).mode & 0o777, 0o700);
  assert.deepEqual(JSON.parse(await readFile(path, 'utf8')), { private: 'value' });
});
async function fixture(t, failure = false) {
  const dir = await temporary(t);
  const sdk = join(dir, 'sdk.mjs');
  await writeFile(sdk, `export class ModelRuntime { static async create(options) { if(options.modelsPath !== null || options.allowModelNetwork !== false) throw Error('unsafe initialization'); return {}; } }
export class ModelRegistry {
 async getAvailableOfType(){ return [{provider:'typesafe',id:'jev-latest',api:'typesafe-system-one',baseUrl:'https://api.typesafe.ai/v1/',input:['text']}]; }
 async classify(model, context, options){ if(options.maxRetries!==0) throw Error('retries'); return {model:model.id, answers:${failure ? '{}' : "{reusable:{type:'bool',probability:0.5}}"},stopReason:'${failure ? 'error' : 'stop'}'${failure ? ",errorMessage:'simulated provider failure'" : ''}}; }
}`);
  const input = join(dir, 'input.json');
  await writeFile(input, JSON.stringify([{ id: 'one', state: { message: 'Evidence' } }]));
  return { dir, sdk, input, cache: join(dir, 'cache') };
}
test('CLI preserves full input/questions and caches successful identical content', async t => {
  const f = await fixture(t);
  const invoke = output => main(['--sdk', f.sdk, '--input', f.input, '--output', output, '--cache-dir', f.cache]);
  const first = join(f.dir, 'first.json'), second = join(f.dir, 'second.json');
  assert.equal(await invoke(first), 0);
  assert.equal(await invoke(second), 0);
  const report = JSON.parse(await readFile(second, 'utf8'));
  assert.equal(report.results[0].cached, true);
  assert.deepEqual(report.results[0].state, { message: 'Evidence' });
  assert.deepEqual(report.results[0].questions, DEFAULT_QUESTIONS);
  await assert.rejects(invoke(first), { code: 'EEXIST' });
});
test('CLI accepts custom bool and choice questions, recording them verbatim', async t => {
  const f = await fixture(t);
  const questions = {
    correction: { type: 'bool', instructions: 'Was an explicit correction made?', criteria: { true: 'Explicit correction', false: 'No correction' } },
    kind: { type: 'choice', instructions: 'What kind of evidence is present?', criteria: { correction: 'Correction', discovery: 'Discovery', other: 'Neither' } },
  };
  const questionPath = join(f.dir, 'questions.json');
  await writeFile(questionPath, JSON.stringify(questions));
  const output = join(f.dir, 'custom.json');
  assert.equal(await main(['--sdk', f.sdk, '--input', f.input, '--output', output, '--questions', questionPath, '--max-items', '800']), 0);
  const report = JSON.parse(await readFile(output, 'utf8'));
  assert.deepEqual(report.questions, questions);
  assert.deepEqual(report.results[0].questions, questions);
});
test('provider errors remain errors, cause nonzero exit, and are not cached', async t => {
  const f = await fixture(t, true);
  for (const name of ['first', 'second']) {
    const output = join(f.dir, `${name}.json`);
    assert.equal(await main(['--sdk', f.sdk, '--input', f.input, '--output', output, '--cache-dir', f.cache]), 2);
    const report = JSON.parse(await readFile(output, 'utf8'));
    assert.equal(report.status, 'error');
    assert.equal(report.results[0].cached, false);
    assert.equal(report.results[0].result.errorMessage, 'simulated provider failure');
  }
});
test('corrupt or permissive cache is reported, never silently replaced', async t => {
  const f = await fixture(t);
  const args = ['--sdk', f.sdk, '--input', f.input, '--cache-dir', f.cache];
  const first = join(f.dir, 'first.json');
  assert.equal(await main([...args, '--output', first]), 0);
  const entry = JSON.parse(await readFile(first, 'utf8')).results[0];
  const cachePath = join(f.cache, `${entry.key}.json`);
  await chmod(cachePath, 0o644);
  const second = join(f.dir, 'second.json');
  assert.equal(await main([...args, '--output', second]), 2);
  assert.match(JSON.parse(await readFile(second, 'utf8')).results[0].error.message, /0600/);
  await chmod(cachePath, 0o600);
  await writeFile(cachePath, 'broken JSON');
  assert.equal(await main([...args, '--output', join(f.dir, 'third.json')]), 2);
  assert.equal(await readFile(cachePath, 'utf8'), 'broken JSON');
});
