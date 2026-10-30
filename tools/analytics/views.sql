-- Metadata only: JSON timestamp numbers retain source units. Only ISO strings
-- become observed timestamps here; no guessing milliseconds versus seconds.
CREATE OR REPLACE VIEW sessions AS
SELECT object_key, body->>'machine_id' AS machine_id,
       body->>'agent' AS agent, body->>'key' AS session_key,
       body->'manifest'->>'hostname' AS hostname,
       body->'manifest'->'info' AS info,
       body->'manifest'->'git' AS git,
       (body->>'sequence')::BIGINT AS generation,
       to_timestamp((body->'manifest'->>'first_seen')::DOUBLE) AS first_seen,
       to_timestamp((body->'manifest'->>'synced_at')::DOUBLE) AS synced_at,
       json_array_length(body->'manifest'->'launches') AS launch_count
FROM current_manifests;

CREATE OR REPLACE VIEW transcripts AS
SELECT m.body->>'machine_id' AS machine_id,
       m.body->>'agent' AS agent, m.body->>'key' AS session_key,
       t.value->>'key' AS file_key, t.value->>'role' AS role,
       (t.value->>'size')::BIGINT AS decoded_bytes,
       (t.value->>'line_count')::BIGINT AS line_count,
       t.value->>'sha256' AS sha256,
       t.value->'head'->>'model' AS model,
       t.value->'head'->>'started_at' AS started_at_raw,
       t.value->'head'->>'last_event_at' AS last_event_at_raw,
       CASE WHEN json_type(t.value->'head', '$.started_at') = 'VARCHAR'
            THEN try_cast(t.value->'head'->>'started_at' AS TIMESTAMPTZ) END AS started_at,
       CASE WHEN json_type(t.value->'head', '$.last_event_at') = 'VARCHAR'
            THEN try_cast(t.value->'head'->>'last_event_at' AS TIMESTAMPTZ) END AS last_event_at,
       t.value->'head'->'lineage' AS lineage,
       t.value->>'commit' AS file_commit
FROM current_manifests m, json_each(m.body->'manifest'->'transcripts') t;
