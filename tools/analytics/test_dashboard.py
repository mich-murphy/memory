"""Security boundaries and evidence navigation for the private dashboard."""
import http.client
import json
from pathlib import Path
import tempfile
import threading
import unittest

import duckdb

from analyze import excerpt, prepare, redact, write_json
from serve import Dashboard


class DashboardTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        root = Path(self.temp.name)
        self.database = root / 'events.duckdb'
        self.raw = root / 'source.jsonl'
        self.records = [{'text': 'hello 🦆'}, {'text': '<script>untrusted</script>'}]
        encoded = [(json.dumps(r, ensure_ascii=False) + '\n').encode() for r in self.records]
        self.raw.write_bytes(b''.join(encoded))
        with duckdb.connect(str(self.database)) as db:
            db.execute('''CREATE TABLE files(trace_id VARCHAR, agent VARCHAR, project VARCHAR, session_key VARCHAR,
                file_key VARCHAR, source_role VARCHAR,path VARCHAR, started_at VARCHAR)''')
            db.execute('INSERT INTO files VALUES (?,?,?,?,?,?,?,?)', ['safe','pi','example','sess','key','main',str(self.raw),'2026-01-01'])
            db.execute('''CREATE TABLE events(event_id VARCHAR,trace_id VARCHAR,line INTEGER,ordinal INTEGER,
                role VARCHAR,text VARCHAR,is_human BOOLEAN,duplicate BOOLEAN,event_type VARCHAR,
                is_error BOOLEAN,byte_offset BIGINT,byte_length BIGINT)''')
            db.executemany('INSERT INTO events VALUES (?,?,?,?,?,?,?,?,?,?,?,?)', [
                ('safe:1:0','safe',1,0,'user','hello 🦆',True,False,'message',None,0,len(encoded[0])),
                ('safe:2:0','safe',2,0,'assistant','<script>untrusted</script>',False,False,'message',None,len(encoded[0]),len(encoded[1]))])
        report = root / 'report.json'
        write_json(report, {'metrics': {'files':1}})
        self.server = Dashboard(('127.0.0.1',0), self.database,report)
        self.thread = threading.Thread(target=self.server.serve_forever,daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.temp.cleanup()

    def request(self, path, headers=None):
        connection = http.client.HTTPConnection('127.0.0.1',self.server.server_port,timeout=5)
        try:
            connection.request('GET',path,headers=headers or {})
            response = connection.getresponse()
            return response.status, dict(response.getheaders()), response.read()
        finally:
            connection.close()

    def test_exact_raw_event_uses_byte_offsets_after_unicode(self):
        status,headers,body = self.request('/api/event?trace=safe&line=2')
        self.assertEqual(status,200)
        self.assertEqual(json.loads(body)['raw'],self.records[1])
        self.assertIn("frame-ancestors 'none'",headers['Content-Security-Policy'])

    def test_citation_centres_trace_and_filters_do_not_execute_sql(self):
        status,_,body = self.request('/api/trace?id=safe&line=2&limit=1')
        self.assertEqual(status,200)
        self.assertEqual(json.loads(body)['focus_line'],2)
        self.assertEqual(json.loads(body)['items'][0]['line'],2)
        status,_,body = self.request('/api/sessions?q=%27%20OR%20TRUE--')
        self.assertEqual(status,200)
        self.assertEqual(json.loads(body)['total'],0)
        status,_,_ = self.request('/api/sessions?limit=-1')
        self.assertEqual(status,400)

    def test_does_not_serve_arbitrary_files_or_accept_rebinding(self):
        for path in ('/../.env','/tools/analytics/serve.py','/api/sql?sql=SELECT%201'):
            status,_,_ = self.request(path)
            self.assertIn(status,(400,404))
        status,_,_ = self.request('/api/report', {'Host':'attacker.example'})
        self.assertEqual(status,403)
        status,_,_ = self.request('/api/report', {'Sec-Fetch-Site':'cross-site'})
        self.assertEqual(status,403)

    def test_malformed_source_is_accessible_without_a_normalized_event(self):
        offset = self.raw.stat().st_size
        with self.raw.open('ab') as source:
            source.write(b'broken JSON\n')
        with duckdb.connect(str(self.database)) as db:
            db.execute('CREATE TABLE issues(trace_id VARCHAR,line INTEGER,"offset" BIGINT,length BIGINT,kind VARCHAR,record_type VARCHAR)')
            db.execute("INSERT INTO issues VALUES ('safe',3,?,12,'malformed_json',NULL)",[offset])
        status,_,body = self.request('/api/event?trace=safe&line=3')
        self.assertEqual(status,200)
        self.assertFalse(json.loads(body)['valid_json'])
        self.assertEqual(json.loads(body)['raw_text'],'broken JSON\n')

    def test_small_candidate_budget_never_exceeds_maximum(self):
        with duckdb.connect(str(self.database)) as db:
            db.execute("UPDATE events SET text='No, do not change the unrelated application.' WHERE role='user'")
            db.execute("UPDATE events SET text='Root cause: this adapter requires the private root.' WHERE role='assistant'")
            output = Path(self.temp.name)/'selection'
            prepare(db,output,maximum=1)
        self.assertEqual(len(json.loads((output/'inputs.json').read_text())),1)

    def test_human_filter_and_unknown_trace(self):
        status,_,body = self.request('/api/trace?id=safe&role=human')
        self.assertEqual(status,200)
        self.assertEqual(json.loads(body)['total'],1)
        status,_,_ = self.request('/api/event?trace=../../.env&line=1')
        self.assertEqual(status,400)

    def test_middle_and_balanced_excerpts_label_omissions(self):
        text = 'H'*1000 + 'VALUE'*200 + 'T'*1000
        middle = excerpt(text,60)
        self.assertIn('VALUE',middle)
        self.assertNotIn('HHHH',middle)
        self.assertIn('leading characters',middle)
        balanced = excerpt(text,90,'balanced')
        self.assertIn('HHHH',balanced)
        self.assertIn('VALUE',balanced)
        self.assertIn('TTTT',balanced)
        self.assertEqual(excerpt('short',100),'short')

    def test_credential_redaction_and_private_report(self):
        self.assertNotIn('sk-abcdefghijklmnop12345',redact('token sk-abcdefghijklmnop12345'))
        root = Path(self.temp.name)/'private'
        write_json(root/'data.json',{'value':'private'})
        self.assertEqual((root/'data.json').stat().st_mode & 0o777,0o600)
        self.assertEqual(root.stat().st_mode & 0o777,0o700)


if __name__ == '__main__':
    unittest.main()
