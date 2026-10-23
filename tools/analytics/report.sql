-- Bucket inventory: stored bytes include retained history, plans and snapshots.
SELECT split_part(key, '/', 2) AS category, count(*) AS objects,
       round(sum(size) / 1048576.0, 2) AS stored_mib
FROM inventory GROUP BY ALL ORDER BY objects DESC;

-- Aggregate transcripts separately so sessions with multiple children count once.
SELECT s.agent, count(*) AS sessions, count(DISTINCT s.machine_id) AS machines,
       sum(coalesce(t.files, 0)) AS transcript_files,
       round(sum(coalesce(t.bytes, 0)) / 1048576.0, 2) AS decoded_mib,
       sum(coalesce(t.lines, 0)) AS jsonl_lines
FROM sessions s LEFT JOIN (
    SELECT machine_id, agent, session_key, count(*) AS files,
           sum(decoded_bytes) AS bytes, sum(line_count) AS lines
    FROM transcripts GROUP BY ALL
) t USING (machine_id, agent, session_key)
GROUP BY s.agent ORDER BY sessions DESC;

-- Main-file model observations, not per-message model usage.
SELECT agent, coalesce(model, '(unobserved)') AS model,
       count(*) AS main_transcripts
FROM transcripts WHERE role = 'main' GROUP BY ALL ORDER BY main_transcripts DESC;

SELECT agent, role, count(*) AS files,
       round(sum(decoded_bytes) / 1048576.0, 2) AS decoded_mib
FROM transcripts GROUP BY ALL ORDER BY agent, files DESC;

-- Bounded head/tail observations are not exact session end times or active duration.
SELECT agent, min(started_at) AS earliest_observed_start,
       max(last_event_at) AS latest_observed_event,
       count(*) FILTER (WHERE started_at IS NOT NULL) AS starts_observed,
       count(*) AS main_transcripts
FROM transcripts WHERE role = 'main' GROUP BY ALL ORDER BY agent;

SELECT strftime(started_at AT TIME ZONE 'UTC', '%Y-%m') AS start_month_utc,
       agent, count(*) AS main_transcripts
FROM transcripts WHERE role = 'main' AND started_at IS NOT NULL
GROUP BY ALL ORDER BY start_month_utc, agent;

-- Logical bytes are not unique storage bytes. Same content can exist on two machines.
SELECT count(*) AS transcript_files, count(DISTINCT sha256) AS distinct_contents,
       round(sum(decoded_bytes) / 1048576.0, 2) AS logical_mib,
       round((SELECT sum(decoded_bytes) FROM (
           SELECT sha256, max(decoded_bytes) AS decoded_bytes FROM transcripts GROUP BY sha256
       )) / 1048576.0, 2) AS unique_content_mib
FROM transcripts;

SELECT agent, count(*) AS sessions,
       count(*) FILTER (WHERE launch_count > 0) AS sessions_with_launches,
       count(*) FILTER (WHERE generation > 1) AS sessions_with_manifest_updates
FROM sessions GROUP BY ALL ORDER BY agent;
