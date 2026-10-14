package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mich-murphy/memory/internal/jsonx"
)

// ValidateCodexHooks checks whole-document admission before individual hooks are counted.
// Codex rejects a malformed foreign handler too. Only the top-level HooksFile denies
// unknown fields; nested unknown fields and unknown events are ignored by its loader.
// See the pinned hook_config.rs sources in docs/install.md.
func ValidateCodexHooks(doc *jsonx.Object) error {
	for _, key := range doc.Keys() {
		if key != "description" && key != "hooks" {
			return errors.New("codex hooks: unknown top-level field")
		}
	}
	if !optionalString(doc, "description") {
		return errors.New("codex hooks: description must be a string or null")
	}
	value, present := doc.Get("hooks")
	if !present {
		return nil
	}
	events, ok := value.(*jsonx.Object)
	if !ok {
		return errors.New("codex hooks: hooks must be an object")
	}
	known := []string{"PreToolUse", "PermissionRequest", "PostToolUse", "PreCompact", "PostCompact", "SessionStart", "SessionEnd", "UserPromptSubmit", "SubagentStart", "SubagentStop", "Stop", "Interrupt"}
	for _, event := range events.Keys() {
		if !slices.Contains(known, event) {
			continue
		}
		value, _ := events.Get(event)
		groups, ok := value.([]any)
		if !ok {
			return fmt.Errorf("codex hooks: %s must be an array", event)
		}
		if slices.ContainsFunc(groups, func(group any) bool { return !codexGroupValid(group) }) {
			return fmt.Errorf("codex hooks: invalid or unsupported %s group", event)
		}
	}
	return nil
}

func codexGroupValid(value any) bool {
	group, ok := value.(*jsonx.Object)
	if !ok || !optionalString(group, "matcher") {
		return false
	}
	value, present := group.Get("hooks")
	if !present {
		return true
	}
	handlers, ok := value.([]any)
	return ok && !slices.ContainsFunc(handlers, func(h any) bool { return !codexHandlerValid(h) })
}

func codexHandlerValid(value any) bool {
	h, ok := value.(*jsonx.Object)
	if !ok {
		return false
	}
	kind, _ := h.Get("type")
	switch kind {
	case "command":
		_, camel := h.Get("commandWindows")
		_, snake := h.Get("command_windows")
		if camel && snake {
			return false
		}
		return requiredString(h, "command") && optionalString(h, "commandWindows") &&
			optionalString(h, "command_windows") && optionalString(h, "statusMessage") &&
			optionalUint(h, "timeout") && optionalUint(h, "additionalContextLimit") && optionalBool(h, "async")
	case "mcp_tool":
		input, present := h.Get("input")
		obj, isObject := input.(*jsonx.Object)
		return requiredString(h, "server") && requiredString(h, "tool") && optionalString(h, "statusMessage") &&
			optionalUint(h, "timeout") && (!present || isObject && tomlValueValid(obj))
	case "prompt", "agent":
		return true
	default:
		return false
	}
}

func requiredString(obj *jsonx.Object, key string) bool {
	value, _ := obj.Get(key)
	_, ok := value.(string)
	return ok
}

func optionalString(obj *jsonx.Object, key string) bool {
	value, _ := obj.Get(key)
	_, ok := value.(string)
	return value == nil || ok
}

func optionalBool(obj *jsonx.Object, key string) bool {
	value, present := obj.Get(key)
	_, ok := value.(bool)
	return !present || ok
}

func optionalUint(obj *jsonx.Object, key string) bool {
	value, _ := obj.Get(key)
	if value == nil {
		return true
	}
	n, ok := value.(json.Number)
	if !ok {
		return false
	}
	_, err := strconv.ParseUint(n.String(), 10, 64)
	return err == nil
}

// MCP input is converted to TOML for Codex's trust identity. Null is not representable.
// Large integer spellings outside TOML's signed range fail closed as unsupported.
func tomlValueValid(value any) bool {
	switch v := value.(type) {
	case string, bool:
		return true
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			_, err := v.Float64()
			return err == nil
		}
		_, err := v.Int64()
		return err == nil
	case []any:
		return !slices.ContainsFunc(v, func(item any) bool { return !tomlValueValid(item) })
	case *jsonx.Object:
		for _, key := range v.Keys() {
			item, _ := v.Get(key)
			if !tomlValueValid(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
