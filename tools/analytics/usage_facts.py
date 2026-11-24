"""Usage and tool outcomes derived from a transcript's immutable bodies (brief 55).

Tables `session_usage` (one row per billable unit) and `tool_outcome` (one row per tool result
that carries an outcome) are derived data, rebuilt whenever setup_facts.SETUP_VERSION changes.
A missing field is NULL, never 0. A token value must be an integer in [0, 2**53), a cost a
finite non-negative number, a duration likewise; an invalid value makes that column NULL and
counts one skip. Never stored: a command, arguments, output, cwd or a path.
"""
import json
import math
from pathlib import Path

import setup_facts

LIMIT = 2 ** 53
MAX_TEXT = 200
COORDINATES = setup_facts.COORDINATES
USAGE_COLUMNS = {'trace_id': 'VARCHAR', 'line': 'BIGINT', 'timestamp': 'VARCHAR', 'model': 'VARCHAR',
                 'input_tokens': 'BIGINT', 'cache_read_tokens': 'BIGINT',
                 'cache_write_tokens': 'BIGINT', 'output_tokens': 'BIGINT',
                 'reasoning_tokens': 'BIGINT', 'cost_usd': 'DOUBLE'}
OUTCOME_COLUMNS = {'trace_id': 'VARCHAR', 'line': 'BIGINT', 'timestamp': 'VARCHAR',
                   'tool_call_id': 'VARCHAR', 'tool_name': 'VARCHAR', 'is_error': 'BOOLEAN',
                   'exit_code': 'BIGINT', 'duration_ms': 'BIGINT'}
TOKENS = ('input_tokens', 'cache_read_tokens', 'cache_write_tokens', 'output_tokens',
          'reasoning_tokens')
TABLES = ('session_usage', 'tool_outcome')


class _Count:
    def __init__(self, skipped):
        self.skipped = skipped

    def skip(self):
        if self.skipped is not None:
            self.skipped['skipped'] = self.skipped.get('skipped', 0) + 1

    def integer(self, value, signed=False):
        if value is None:
            return None
        if type(value) is int and (abs(value) if signed else value) < LIMIT and (signed or value >= 0):
            return value
        self.skip()
        return None

    def number(self, value):
        if value is None:
            return None
        if type(value) in (int, float) and math.isfinite(value) and 0 <= value < LIMIT:
            return float(value)
        self.skip()
        return None

    def text(self, value):
        if value is None:
            return None
        if isinstance(value, str) and len(value) <= MAX_TEXT:
            try:
                value.encode('utf-8')
                return value
            except UnicodeError:
                pass
        self.skip()
        return None

    def flag(self, value):
        if value is None:
            return None
        if isinstance(value, bool):
            return value
        self.skip()
        return None


def _d(value):
    return value if isinstance(value, dict) else {}


def _list(value):
    return value if isinstance(value, list) else []


def _usage_row(c, **kw):
    return kw


def _claude_usage(c, r, ctx):
    m = _d(r.get('message'))
    u = m.get('usage')
    if r.get('type') != 'assistant' or not isinstance(u, dict):
        return []
    mid = m.get('id')
    if isinstance(mid, str) and mid:
        if mid in ctx.setdefault('ids', set()):
            return []
        ctx['ids'].add(mid)
    return [('session_usage', dict(
        input_tokens=c.integer(u.get('input_tokens')),
        cache_read_tokens=c.integer(u.get('cache_read_input_tokens')),
        cache_write_tokens=c.integer(u.get('cache_creation_input_tokens')),
        output_tokens=c.integer(u.get('output_tokens')),
        reasoning_tokens=c.integer(_d(u.get('output_tokens_details')).get('thinking_tokens')),
        cost_usd=None))]


def _claude_outcome(c, r, ctx):
    m = _d(r.get('message'))
    blocks = [b for b in _list(m.get('content')) if isinstance(b, dict)]
    t = r.get('type')
    if t == 'assistant':
        for b in blocks:
            if b.get('type') == 'tool_use' and isinstance(b.get('id'), str) \
                    and isinstance(b.get('name'), str):
                ctx.setdefault('names', {})[b['id']] = b['name']
        return []
    results = [b for b in blocks if b.get('type') == 'tool_result']
    if t != 'user' or len(results) != 1:
        return []
    b, tur = results[0], _d(r.get('toolUseResult'))
    duration = c.integer(tur.get('durationMs'))
    if duration is None and tur.get('durationSeconds') is not None:
        seconds = c.number(tur.get('durationSeconds'))
        duration = None if seconds is None else round(seconds * 1000)
        duration = c.integer(duration)
    call = c.text(b.get('tool_use_id'))
    row = dict(tool_call_id=call, tool_name=ctx.get('names', {}).get(call),
               is_error=c.flag(b.get('is_error')), exit_code=None, duration_ms=duration)
    return [('tool_outcome', row)]


def _codex_usage(c, r, ctx):
    p = _d(r.get('payload'))
    if r.get('type') != 'event_msg' or p.get('type') != 'token_count' \
            or not isinstance(p.get('info'), dict):
        return []
    info = p['info']
    total = c.integer(_d(info.get('total_token_usage')).get('total_tokens'))
    if total is None or total == ctx.get('total'):
        return []
    ctx['total'] = total
    last = _d(info.get('last_token_usage'))
    raw_in, cached = c.integer(last.get('input_tokens')), c.integer(last.get('cached_input_tokens'))
    uncached = None
    if raw_in is not None and cached is not None:
        if raw_in - cached < 0:
            c.skip()
        else:
            uncached = raw_in - cached
    return [('session_usage', dict(
        input_tokens=uncached, cache_read_tokens=cached,
        cache_write_tokens=c.integer(last.get('cache_write_input_tokens')),
        output_tokens=c.integer(last.get('output_tokens')),
        reasoning_tokens=c.integer(last.get('reasoning_output_tokens')), cost_usd=None))]


def _codex_ms(c, duration):
    if not isinstance(duration, dict):
        return None
    secs, nanos = c.integer(duration.get('secs')), c.integer(duration.get('nanos'))
    if secs is None or nanos is None:
        return None
    return c.integer(secs * 1000 + nanos // 10 ** 6)


def _codex_outcome(c, r, ctx):
    p = _d(r.get('payload'))
    item = _d(p.get('item'))
    kind = item.get('type')
    if r.get('type') != 'event_msg' or p.get('type') != 'item_completed' \
            or kind not in ('CommandExecution', 'McpToolCall'):
        return []
    mcp = kind == 'McpToolCall'
    return [('tool_outcome', dict(
        tool_call_id=c.text(item.get('id')) if mcp else None, tool_name=kind, is_error=None,
        exit_code=c.integer(item.get('exit_code'), True), duration_ms=_codex_ms(c, item.get('duration'))))]


def _pi_usage_row(c, u):
    u = _d(u)
    return ('session_usage', dict(
        input_tokens=c.integer(u.get('input')), cache_read_tokens=c.integer(u.get('cacheRead')),
        cache_write_tokens=c.integer(u.get('cacheWrite')), output_tokens=c.integer(u.get('output')),
        reasoning_tokens=c.integer(u.get('reasoning')),
        cost_usd=c.number(_d(u.get('cost')).get('total'))))


def _pi_usage(c, r, ctx):
    m = _d(r.get('message'))
    if r.get('type') == 'message' and m.get('role') == 'assistant' and isinstance(m.get('usage'), dict):
        return [_pi_usage_row(c, m['usage'])]
    if r.get('type') == 'compaction' and isinstance(r.get('usage'), dict):
        return [_pi_usage_row(c, r['usage'])]
    return []


def _pi_outcome(c, r, ctx):
    m = _d(r.get('message'))
    role = m.get('role') if r.get('type') == 'message' else r.get('type')
    if role == 'toolResult':
        d = m.get('durationMs', r.get('durationMs'))
        return [('tool_outcome', dict(
            tool_call_id=c.text(m.get('toolCallId')), tool_name=c.text(m.get('toolName')),
            is_error=c.flag(m.get('isError')), exit_code=None, duration_ms=c.integer(d)))]
    if role == 'bashExecution':
        src = m if m else r
        return [('tool_outcome', dict(tool_call_id=None, tool_name='bash', is_error=None,
                                      exit_code=c.integer(src.get('exitCode'), True),
                                      duration_ms=None))]
    return []


_AGENTS = {'claude': (_claude_usage, _claude_outcome), 'codex': (_codex_usage, _codex_outcome),
           'pi': (_pi_usage, _pi_outcome)}


def _model(r):
    p = _d(r.get('payload'))
    v = _d(r.get('message')).get('model', r.get('model', r.get('modelId', p.get('model'))))
    return v if isinstance(v, str) else None


def iter_usage(path, agent, trace, skipped=None):
    """Yield (table, row) pairs; `skipped` (a dict) counts values that were not stored."""
    extract = _AGENTS.get(agent)
    if extract is None:
        return
    c = _Count(skipped)
    ctx, model = {}, None
    with Path(path).open('rb') as source:
        for number, raw in enumerate(source, 1):
            try:
                r = json.loads(raw)
                if not isinstance(r, dict):
                    continue
                model = _model(r) or model
                found = extract[0](c, r, ctx) + extract[1](c, r, ctx)
            except (ValueError, RecursionError):
                continue
            ts = r.get('timestamp')
            if not (isinstance(ts, str) and len(ts) <= MAX_TEXT):
                ts = None
            else:
                try:
                    ts.encode('utf-8')
                except UnicodeError:
                    ts = None
            for table, row in found:
                columns = USAGE_COLUMNS if table == 'session_usage' else OUTCOME_COLUMNS
                row = dict(row, trace_id=trace, line=number, timestamp=ts)
                if table == 'session_usage':
                    row['model'] = c.text(model)
                    if all(row[k] is None for k in TOKENS + ('cost_usd',)):
                        continue
                elif all(row[k] is None for k in ('is_error', 'exit_code', 'duration_ms')):
                    continue
                yield table, {k: row.get(k) for k in columns}


def _sum(column):
    return f'sum({column})'


def ensure(db):
    """Writable-open only: the tables, `session_dim` (setup plus usage) and `usage_by_model`."""
    for table, columns in (('session_usage', USAGE_COLUMNS), ('tool_outcome', OUTCOME_COLUMNS)):
        spec = dict({k: 'VARCHAR' for k in COORDINATES}, **columns)
        db.execute(f'CREATE TABLE IF NOT EXISTS {table} (' +
                   ','.join(f'"{k}" {v}' for k, v in spec.items()) + ')')
    sums = ','.join(f'sum({k}) AS {k}' for k in TOKENS)
    used = ','.join(f"count(DISTINCT name) FILTER (WHERE fact='{f}_used') AS {f}s_used"
                    for f in ('skill', 'plugin', 'mcp_server', 'extension'))
    db.execute(f'''CREATE OR REPLACE VIEW session_dim AS
        SELECT d.*, u.input_tokens, u.cache_read_tokens, u.cache_write_tokens, u.output_tokens,
            u.reasoning_tokens, u.cost_usd,
            coalesce(t.tool_results, 0) AS tool_results, coalesce(t.tool_errors, 0) AS tool_errors,
            t.tool_duration_ms,
            coalesce(x.skills_used, 0) AS skills_used, coalesce(x.plugins_used, 0) AS plugins_used,
            coalesce(x.mcp_servers_used, 0) AS mcp_servers_used,
            coalesce(x.extensions_used, 0) AS extensions_used
        FROM session_setup_dim d
        LEFT JOIN (SELECT session_key, {sums}, sum(cost_usd) AS cost_usd FROM session_usage
                   GROUP BY session_key) u ON u.session_key = d.session_key
        LEFT JOIN (SELECT session_key, count(*) AS tool_results,
                   count(*) FILTER (WHERE is_error) AS tool_errors,
                   sum(duration_ms) AS tool_duration_ms FROM tool_outcome
                   GROUP BY session_key) t ON t.session_key = d.session_key
        LEFT JOIN (SELECT session_key, {used} FROM session_setup GROUP BY session_key) x
            ON x.session_key = d.session_key''')
    db.execute(f'''CREATE OR REPLACE VIEW usage_by_model AS
        SELECT agent, model, count(DISTINCT session_key) AS sessions, {sums},
            sum(cost_usd) AS cost_usd FROM session_usage GROUP BY agent, model''')
