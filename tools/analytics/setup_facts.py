"""Agent setup facts recorded in a transcript at the time the session ran (brief 53).

Events are the fact table; the setup of a session is a dimension derived from its immutable
bodies, so it is rebuilt (never patched) whenever SETUP_VERSION changes. Extraction is
tolerant (schema-on-read) and the projection is strict (schema-on-write): the fact list is
closed, names and values are short strings, and the content never includes a prompt,
instruction text, tool input or output, path, directory or cwd.

Brief 55 adds the used facts; usage and tool outcomes live in usage_facts.py.

Table `session_setup` is long format: the coordinate columns of `events` plus trace_id,
line, timestamp, fact, name, value. One row is kept per first sighting of each
(fact, name, value) within a source; a repeat is skipped.
"""
import json
import re
from pathlib import Path

# Version 2 (brief 55): adds the used facts and the usage and tool-outcome tables; one version
# row per session governs every derived side table. Version 3 (brief 56): adds pi_run_ref.
SETUP_VERSION = 3
MAX_TEXT = 200

FACTS = frozenset((
    'harness', 'harness_version', 'session_format', 'entrypoint', 'originator', 'model',
    'provider', 'reasoning_effort', 'permission_mode', 'approval_policy', 'sandbox_policy',
    'collaboration_mode', 'skill_available', 'skill_invoked', 'subagent_type_available',
    'tool_available', 'tool_allowed', 'mcp_server_available', 'mcp_server_failed',
    'plugin_disabled', 'instruction_file', 'skill_used', 'plugin_used', 'mcp_server_used',
    'extension_used'))

# Used facts carry only an identifier-like name (brief 55); anything else is skipped and counted.
# Parent (brief 55 real-data run): claude.ai connectors are named with single internal spaces
# (5 names, 1,959 uses here, the same strings brief 53 stores as `mcp_server_available`), so words
# of identifier characters joined by one space are allowed; at most 100 characters.
USED = frozenset(('skill_used', 'plugin_used', 'mcp_server_used', 'extension_used'))
USED_NAME = re.compile(r'(?=.{1,100}\Z)[A-Za-z0-9._:@-]+(?: [A-Za-z0-9._:@-]+)*', re.DOTALL)

SETUP_COLUMNS = {'trace_id': 'VARCHAR', 'line': 'BIGINT', 'timestamp': 'VARCHAR',
                 'fact': 'VARCHAR', 'name': 'VARCHAR', 'value': 'VARCHAR'}
COORDINATES = ('session_key', 'archive_id', 'machine_id', 'agent', 'session_id',
               'source_id', 'epoch_id', 'publication_id')

# Only these base names are stored; any other name (a rule or nested file) can name a project.
INSTRUCTION_NAMES = frozenset(('CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md',
                               'AGENTS.override.md', 'GEMINI.md'))
SCOPES = frozenset(('project', 'user', 'local', 'managed'))


def _list(value):
    return value if isinstance(value, list) else []


def _get(d, *path):
    for p in path:
        d = d.get(p) if isinstance(d, dict) else None
    return d


def _opt(d, key):
    """The raw value when present; the validator rejects a non-string and counts it."""
    return d.get(key) if isinstance(d, dict) else None


def _scalar(out, fact, source, key, as_name=False):
    value = _opt(source, key)
    if value is not None:
        out.append((fact, value, '') if as_name else (fact, '', value))


def _instruction_name(path):
    if not isinstance(path, str):
        return path  # rejected and counted by the validator
    base = re.split(r'[\\/]', path)[-1]
    return base if base in INSTRUCTION_NAMES else 'other'


def _leaf(name):
    """A plugin skill name carries a relative directory (`.x/y/plugin:skill`); keep the last
    segment only, so no directory is stored."""
    return re.split(r'[\\/]', name)[-1] if isinstance(name, str) else name


def _tool(pattern):
    """A permission pattern `Tool(argument)` can hold a path or command; keep the tool name."""
    return pattern.split('(', 1)[0].strip() if isinstance(pattern, str) else pattern


# Outside these facts a path character means a path slipped through: skip and count it.
PATH_OK = frozenset(('model',))


def _scope(kind):
    kind = kind.lower() if isinstance(kind, str) else ''
    return kind if kind in SCOPES else 'other'


def _claude(r, ctx):
    out = []
    _scalar(out, 'harness_version', r, 'version')
    _scalar(out, 'entrypoint', r, 'entrypoint')
    t = r.get('type')
    a = r.get('attachment') if isinstance(r.get('attachment'), dict) else {}
    at = a.get('type')
    if t == 'assistant':
        _scalar(out, 'model', r.get('message'), 'model', True)
        _scalar(out, 'reasoning_effort', r, 'effort')
        _scalar(out, 'skill_used', r, 'attributionSkill', True)
        _scalar(out, 'plugin_used', r, 'attributionPlugin', True)
        _scalar(out, 'mcp_server_used', r, 'attributionMcpServer', True)
    elif t == 'permission-mode':
        _scalar(out, 'permission_mode', r, 'permissionMode')
    elif at == 'model':
        _scalar(out, 'model', a.get('identity'), 'modelId', True)
    elif at == 'skill_listing':
        out += [('skill_available', _leaf(n), '') for n in _list(a.get('names'))]
    elif at == 'agent_listing_delta':
        out += [('subagent_type_available', n, '') for n in _list(a.get('addedTypes'))]
    elif at == 'deferred_tools_delta':
        out += [('tool_available', n, '') for n in _list(a.get('addedNames'))]
        out += [('mcp_server_failed', _opt(s, 'name'), '') for s in _list(a.get('failedMcpServers'))]
    elif at == 'mcp_instructions_delta':
        out += [('mcp_server_available', n, '') for n in _list(a.get('addedNames'))]
    elif at == 'instructions':
        for f in _list(a.get('files')):
            if isinstance(f, dict) and 'path' in f:
                out.append(('instruction_file', _instruction_name(f['path']), _scope(f.get('type'))))
    elif at == 'invoked_skills':
        out += [('skill_invoked', _leaf(x.get('name') if isinstance(x, dict) else x), '')
                for x in _list(a.get('skills'))]
    elif at == 'command_permissions':
        out += [('tool_allowed', _tool(n), '') for n in _list(a.get('allowedTools'))]
    return out


def _codex(r, ctx):
    p = r.get('payload') if isinstance(r.get('payload'), dict) else {}
    out = []
    t = r.get('type')
    if t == 'session_meta':
        ctx['cwd'] = p.get('cwd')
        for k, f in (('cli_version', 'harness_version'), ('model_provider', 'provider'),
                     ('originator', 'originator')):
            _scalar(out, f, p, k)
        if isinstance(p.get('source'), dict):
            out.append(('entrypoint', '', 'subagent'))
        else:
            _scalar(out, 'entrypoint', p, 'source')
    elif t == 'turn_context':
        _scalar(out, 'model', p, 'model', True)
        _scalar(out, 'reasoning_effort', p, 'effort')
        # A structured (non-string) policy is Codex's granular form; an absent one records nothing.
        ap = p.get('approval_policy')
        if ap is not None:
            out.append(('approval_policy', '', ap if isinstance(ap, str) else 'granular'))
        _scalar(out, 'sandbox_policy', p.get('sandbox_policy'), 'type')
        _scalar(out, 'collaboration_mode', p.get('collaboration_mode'), 'mode')
        out += [('plugin_disabled', n, '') for n in _list(p.get('disabled_plugin_ids'))]
    elif t == 'event_msg' and p.get('type') == 'item_completed':
        item = p.get('item') if isinstance(p.get('item'), dict) else {}
        _scalar(out, 'plugin_used', item, 'pluginId', True)
        if item.get('type') == 'McpToolCall':
            _scalar(out, 'mcp_server_used', item, 'server', True)
            for src in _list(_get(item, 'metadata', 'mcp_attribution', 'sources')):
                _scalar(out, 'mcp_server_used', src, 'server_name', True)
    if t in ('turn_context', 'world_state'):
        # Real rollouts carry it in `world_state` records at payload.state.agents_md (parent,
        # brief 53 real-data run); the turn_context form is kept for tolerance.
        agents = p.get('agents_md', _get(p, 'state', 'agents_md'))
        cwd = ctx.get('cwd')
        for item in agents if isinstance(agents, list) else [agents]:
            directory = _opt(item, 'directory')
            text = _opt(item, 'text')
            if (isinstance(directory, str) and directory) or (isinstance(text, str) and text):
                same = isinstance(cwd, str) and bool(cwd) and directory == cwd
                out.append(('instruction_file', 'AGENTS.md', 'project' if same else 'other'))
    return out


def _skill_dir(path):
    """The parent directory's name of a `.../SKILL.md` path; never the path itself."""
    parts = re.split(r'[\\/]', path) if isinstance(path, str) else []
    return parts[-2] if len(parts) >= 2 and parts[-1] == 'SKILL.md' else None


def _pi(r, ctx):
    t = r.get('type')
    out = []
    if 'customType' in r:
        ct = r['customType']
        out.append(('extension_used', ct.split(':', 1)[0] if isinstance(ct, str) else ct, ''))
    if t == 'session':
        _scalar(out, 'session_format', r, 'version')  # the session format, not the Pi release
    elif t == 'model_change':
        _scalar(out, 'model', r, 'modelId', True)
        _scalar(out, 'provider', r, 'provider')
    elif t == 'thinking_level_change':
        _scalar(out, 'reasoning_effort', r, 'thinkingLevel')
    elif t == 'message' and _get(r, 'message', 'role') == 'assistant':
        m = r['message']
        _scalar(out, 'model', m, 'model', True)
        _scalar(out, 'provider', m, 'provider')
        _scalar(out, 'reasoning_effort', m, 'thinkingLevel')
        for b in _list(m.get('content')):
            if isinstance(b, dict) and b.get('type') == 'toolCall' and b.get('name') == 'read':
                name = _skill_dir(_get(b, 'arguments', 'path'))
                if name is not None:
                    out.append(('skill_used', name, ''))
    return out


_AGENTS = {'claude': _claude, 'codex': _codex, 'pi': _pi}


def _normal(fact, value):
    if fact == 'session_format' and isinstance(value, int) and not isinstance(value, bool):
        return str(value)
    return value


def _valid(fact, name, value):
    """A closed fact and short, encodable strings; anything else is skipped and counted."""
    if fact not in FACTS:
        return False
    for text in (name, value):
        if not isinstance(text, str) or len(text) > MAX_TEXT:
            return False
        try:
            text.encode('utf-8')
        except UnicodeError:
            return False
        if fact not in PATH_OK and ('/' in text or '\\' in text or text.startswith('~')):
            return False
    if fact in USED and not USED_NAME.fullmatch(name):
        return False
    return fact == 'harness' or bool(name or value)


def iter_setup(path, agent, trace, skipped=None):
    """Yield change-only setup rows; `skipped` (a dict) counts what was not stored.

    A malformed line is skipped silently (events.py already issues it).
    """
    extract = _AGENTS.get(agent)
    if extract is None:
        return
    seen, ctx, started = set(), {}, False
    with Path(path).open('rb') as source:
        for number, raw in enumerate(source, 1):
            try:
                r = json.loads(raw)
                if not isinstance(r, dict):
                    continue
                facts = extract(r, ctx)
            except (ValueError, RecursionError):
                continue
            ts = r.get('timestamp')
            try:
                if not isinstance(ts, str) or len(ts) > MAX_TEXT:
                    raise UnicodeError
                ts.encode('utf-8')
            except UnicodeError:
                ts = None
            if not started:
                started = True
                facts = [('harness', agent, '')] + facts
            for fact, name, value in facts:
                value = _normal(fact, value)
                if not _valid(fact, name, value):
                    if skipped is not None:
                        skipped['skipped'] = skipped.get('skipped', 0) + 1
                    continue
                key = (fact, name, value)
                if key in seen:
                    continue
                seen.add(key)
                yield dict(trace_id=trace, line=number, timestamp=ts, fact=fact, name=name, value=value)


def _ordered(fact):
    return f"list(value ORDER BY timestamp NULLS LAST, trace_id, line) FILTER (WHERE fact='{fact}')"


def _names(fact):
    return f"list_sort(list(DISTINCT name) FILTER (WHERE fact='{fact}'))"


def _count(fact):
    return f"count(DISTINCT name) FILTER (WHERE fact='{fact}')"


def ensure(db):
    """Writable-open only: create the tables and (re)create the setup view (`session_dim` is
    completed by usage_facts.ensure); never called read-only."""
    spec = dict({k: 'VARCHAR' for k in COORDINATES}, **SETUP_COLUMNS)
    db.execute('CREATE TABLE IF NOT EXISTS session_setup (' +
               ','.join(f'"{k}" {v}' for k, v in spec.items()) + ')')
    db.execute('CREATE TABLE IF NOT EXISTS session_setup_version(session_key VARCHAR, version INTEGER)')
    db.execute(f'''CREATE OR REPLACE VIEW session_setup_dim AS
        SELECT s.session_key, s.agent,
            f.harness_version, f.session_format, f.entrypoint,
            coalesce(f.models, []::VARCHAR[]) AS models,
            coalesce(f.providers, []::VARCHAR[]) AS providers,
            f.first_reasoning_effort, f.last_reasoning_effort,
            f.permission_mode, f.approval_policy, f.sandbox_policy,
            coalesce(f.skills_available, 0) AS skills_available,
            coalesce(f.mcp_servers_available, 0) AS mcp_servers_available,
            coalesce(f.tools_available, 0) AS tools_available,
            coalesce(f.instruction_files_available, 0) AS instruction_files_available,
            coalesce(f.instruction_file_names, []::VARCHAR[]) AS instruction_file_names
        FROM sessions s LEFT JOIN (
            SELECT session_key,
                ({_ordered('harness_version')})[1] AS harness_version,
                ({_ordered('session_format')})[1] AS session_format,
                ({_ordered('entrypoint')})[1] AS entrypoint,
                {_names('model')} AS models,
                {_names('provider')} AS providers,
                ({_ordered('reasoning_effort')})[1] AS first_reasoning_effort,
                ({_ordered('reasoning_effort')})[-1] AS last_reasoning_effort,
                ({_ordered('permission_mode')})[1] AS permission_mode,
                ({_ordered('approval_policy')})[1] AS approval_policy,
                ({_ordered('sandbox_policy')})[1] AS sandbox_policy,
                {_count('skill_available')} AS skills_available,
                {_count('mcp_server_available')} AS mcp_servers_available,
                {_count('tool_available')} AS tools_available,
                count(DISTINCT name || '/' || value) FILTER (WHERE fact='instruction_file')
                    AS instruction_files_available,
                {_names('instruction_file')} AS instruction_file_names
            FROM session_setup GROUP BY session_key) f ON f.session_key = s.session_key''')
