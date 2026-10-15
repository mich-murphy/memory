package syncer

import (
	"bytes"
	"encoding/json"

	"github.com/mich-murphy/memory/internal/adapters"
	"github.com/mich-murphy/memory/internal/raw"
)

const metadataWindow = 1 << 20

// metadataTail keeps complete lines inside a bounded window, never a fragment of a
// huge event at its left boundary. The byte before the window establishes that boundary.
func metadataTail(data []byte, offset int64) []byte {
	if len(data) > metadataWindow+1 {
		offset += int64(len(data) - metadataWindow - 1)
		data = data[len(data)-metadataWindow-1:]
	}
	if offset > 0 || len(data) > metadataWindow {
		if nl := bytes.IndexByte(data, '\n'); nl >= 0 {
			return data[nl+1:]
		}
		return nil
	}
	return data
}

func (s *Syncer) sessionHead(sf adapters.SessionFile, c *content, previous *raw.Generation) map[string]any {
	head := adapters.ReadHead(c.first[:min(len(c.first), metadataWindow)], prefixLines)
	// Bound both bytes and object count. Read backwards so busy sessions still describe
	// their latest events, not the oldest lines in the tail window.
	tail := c.last
	end := len(tail)
	for range 256 {
		if end == 0 {
			break
		}
		end = bytes.LastIndexByte(tail[:end-1], '\n') + 1
		if end == 0 {
			break
		}
	}
	if end > 0 {
		tail = tail[end:]
	}
	events := adapters.ReadHead(tail, 256)
	info := adapters.SessionHead(s.adapters[sf.Agent], sf, head, events)
	info["metadata_version"] = json.Number("1")
	// A model change can be older than both bounded windows. Only a proven append
	// permits retaining the previous observation; rewrites must not inherit stale data.
	if previous != nil && c.appended {
		recent := adapters.SessionHead(s.adapters[sf.Agent], sf, nil, events)
		for _, key := range []string{"model", "last_event_at"} {
			if _, ok := recent[key]; !ok {
				if value, known := previous.Head[key]; known {
					info[key] = value
				}
			}
		}
	}
	return info
}
