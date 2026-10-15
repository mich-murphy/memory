package adapters

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// SessionHead enriches bounded observations of captured bytes. Tail is a set of complete
// lines only; it must not contain the fragment crossing the bounded window's start.
func SessionHead(a Adapter, sf SessionFile, head, tail []map[string]any) map[string]any {
	if sf.Agent == "codex" {
		// codexHead observes both windows after filtering replayed parent headers.
		return codexHead(a, sf, head, tail)
	}
	info := a.SessionInfo(head)
	observeEvents(sf.Agent, info, tail)
	if sf.Agent == "pi" && sf.Role == RoleSubagent {
		piChildLineage(info, sf.Key)
	}
	return info
}

// A fork can replay headers before or after its own header. Match the rollout identity
// rather than depending on record order, including when its own header is in the tail.
func codexHead(a Adapter, sf SessionFile, head, tail []map[string]any) map[string]any {
	combined := append(append([]map[string]any{}, head...), tail...)
	match := codexRolloutRE.FindStringSubmatch(filepath.Base(sf.Path))
	if len(match) > 1 {
		combined = ownCodexEvents(combined, match[1])
	}
	info := a.SessionInfo(combined)
	ownHeader := false
	for _, event := range combined {
		if event["type"] == "session_meta" {
			ownHeader = true
		}
	}
	if !ownHeader {
		delete(info, "started_at")
	}
	return info
}

func ownCodexEvents(events []map[string]any, id string) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		payload, _ := event["payload"].(map[string]any)
		if event["type"] != "session_meta" || str(payload, "id") == id {
			out = append(out, event)
		}
	}
	return out
}

// A validated child path proves its grouping parent. An explicit header parent supplies
// the identity when present; the path still distinguishes task, fork and run relations.
func piChildLineage(info map[string]any, key string) {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		match := piSessionDirRE.FindStringSubmatch(part)
		if len(match) < 2 || i+1 >= len(parts) {
			continue
		}
		relation := "run"
		switch parts[i+1] {
		case "tasks":
			relation = "task"
		case "forks":
			relation = "fork"
		}
		parent := match[1]
		if lineage, ok := info["lineage"].(map[string]any); ok {
			parent = str(lineage, "parent_session_id")
		}
		setLineage(info, relation, parent)
		return
	}
}

func setLineage(info map[string]any, relation, parent string) {
	if parent != "" {
		info["lineage"] = map[string]any{"relation": relation, "parent_session_id": parent}
	}
}

func piParentID(parent string) string {
	if uuidPrefixRE.FindString(parent) == parent && parent != "" {
		return parent
	}
	name := strings.TrimSuffix(filepath.Base(parent), ".jsonl")
	if match := piSessionDirRE.FindStringSubmatch(name); len(match) > 1 {
		return match[1]
	}
	return ""
}

// Timestamp strings are normalised to UTC. Numeric source timestamps remain exact
// JSON numbers: do not round via float64 or guess seconds versus milliseconds.
func eventTime(value any) any {
	switch v := value.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t.UTC().Format(time.RFC3339Nano)
		}
	case json.Number:
		if json.Valid([]byte(v)) {
			return v
		}
	}
	return nil
}

func setStarted(info map[string]any, value any) {
	if timestamp := eventTime(value); timestamp != nil {
		info["started_at"] = timestamp
	}
}

// A header timestamp is the session start; otherwise started_at is the first known
// event in the bounded head. last_event_at describes an observation, never an end time.
func observeEvents(agent string, info map[string]any, events []map[string]any) {
	for _, event := range events {
		model, known := eventModel(agent, event)
		if !known {
			continue
		}
		if timestamp := eventTime(event["timestamp"]); timestamp != nil {
			info["last_event_at"] = timestamp
		}
		if model != "" {
			info["model"] = model
		}
	}
}

func eventModel(agent string, event map[string]any) (string, bool) {
	kind := str(event, "type")
	switch agent {
	case "pi":
		switch kind {
		case "model_change":
			return str(event, "modelId"), true
		case "message":
			message, _ := event["message"].(map[string]any)
			return str(message, "model"), true
		case "session", "thinking_level_change", "compaction", "branch_summary", "custom", "custom_message", "session_info", "label":
			return "", true
		}
	case "claude":
		switch kind {
		case "assistant", "user", "system", "progress", "summary", "file-history-snapshot", "queue-operation":
			message, _ := event["message"].(map[string]any)
			return str(message, "model"), true
		}
	case "codex":
		payload, _ := event["payload"].(map[string]any)
		switch kind {
		case "turn_context":
			return str(payload, "model"), true
		case "session_meta", "response_item", "event_msg", "compacted":
			return "", true
		}
	}
	return "", false
}

func firstEvent(agent string, info map[string]any, events []map[string]any) {
	if _, ok := info["started_at"]; ok {
		return
	}
	for _, event := range events {
		if _, known := eventModel(agent, event); known {
			if timestamp := eventTime(event["timestamp"]); timestamp != nil {
				info["started_at"] = timestamp
				return
			}
		}
	}
}
