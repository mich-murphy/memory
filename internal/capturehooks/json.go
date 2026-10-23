package capturehooks

import (
	"bytes"
	"errors"
	"strings"

	"github.com/mich-murphy/memory/internal/jsonx"
)

type event struct {
	name    string
	async   bool
	timeout int
}

// Kept literal to avoid invoking/importing the legacy install builder.
func events(agent string) []event {
	if agent == "claude" {
		return []event{{"SessionStart", true, 30}, {"Stop", true, 30}, {"StopFailure", true, 30}, {"SubagentStop", true, 30}, {"PreCompact", true, 30}, {"TaskCompleted", true, 30}, {"TeammateIdle", true, 30}, {"SessionEnd", false, 5}}
	}
	return []event{{"SessionStart", true, 30}, {"Stop", true, 30}, {"SubagentStop", true, 30}, {"PreCompact", true, 30}, {"Interrupt", true, 3}, {"SessionEnd", false, 3}}
}

func editJSON(before []byte, exists bool, agent, command string, remove bool) ([]byte, error) {
	doc := jsonx.NewObject()
	var err error
	if exists {
		doc, err = jsonx.ParseObject(before)
		if err != nil {
			return nil, errors.New("invalid native hook settings JSON")
		}
	}
	original, err := jsonx.Compact(doc)
	if err != nil {
		return nil, errors.New("invalid native hook settings")
	}
	hooks := jsonx.NewObject()
	if v, ok := doc.Get("hooks"); ok {
		var valid bool
		hooks, valid = v.(*jsonx.Object)
		if !valid {
			return nil, errors.New("native hooks must be an object")
		}
	}
	initiallyEmpty := hooks.Len() == 0
	selected := map[string]event{}
	if !remove {
		for _, e := range events(agent) {
			selected[e.name] = e
		}
	}
	if err := updateExisting(hooks, command, selected); err != nil {
		return nil, err
	}

	for _, e := range events(agent) {
		if _, ok := selected[e.name]; !ok {
			continue
		}
		h := handler(jsonx.NewObject(), e, command)
		g := jsonx.NewObject()
		g.Set("hooks", []any{h})
		v, _ := hooks.Get(e.name)
		groups, _ := v.([]any)
		hooks.Set(e.name, append(groups, g))
	}
	if hooks.Len() > 0 {
		doc.Set("hooks", hooks)
	} else if !initiallyEmpty {
		doc.Delete("hooks")
	}
	compact, err := jsonx.Compact(doc)
	if err != nil {
		return nil, errors.New("cannot encode native hook settings")
	}
	if bytes.Equal(original, compact) {
		return before, nil
	}
	return append(compact, '\n'), nil
}

func updateExisting(hooks *jsonx.Object, command string, selected map[string]event) error {
	for _, name := range hooks.Keys() {
		v, _ := hooks.Get(name)
		groups, ok := v.([]any)
		if !ok {
			return errors.New("native hook event must be an array")
		}
		groups, err := editGroups(groups, name, command, selected)
		if err != nil {
			return err
		}
		if len(groups) == 0 {
			hooks.Delete(name)
		} else {
			hooks.Set(name, groups)
		}
	}
	return nil
}

func handler(h *jsonx.Object, e event, command string) *jsonx.Object {
	h = h.Clone()
	h.Set("type", "command")
	h.Set("command", command)
	h.Set("timeout", e.timeout)
	if e.async {
		h.Set("async", true)
	} else {
		h.Delete("async")
	}
	return h
}

func editGroups(groups []any, name, command string, selected map[string]event) ([]any, error) {
	var out []any
	for _, v := range groups {
		g, ok := v.(*jsonx.Object)
		if !ok {
			return nil, errors.New("native hook group must be an object")
		}
		raw, ok := g.Get("hooks")
		if !ok {
			return nil, errors.New("native hook group missing handlers")
		}
		handlers, ok := raw.([]any)
		if !ok {
			return nil, errors.New("native hook handlers must be an array")
		}
		kept, err := editHandlers(handlers, name, command, selected)
		if err != nil {
			return nil, err
		}

		// Drop only an empty group with no unknown fields. Foreign matchers and
		// extension fields survive even if the group's sole candidate was removed.
		if len(kept) == 0 && len(handlers) > 0 && g.Len() == 1 {
			continue
		}
		g = g.Clone()
		g.Set("hooks", kept)
		out = append(out, g)
	}
	return out, nil
}

func editHandlers(handlers []any, name, command string, selected map[string]event) ([]any, error) {
	kept := make([]any, 0, len(handlers))
	for _, v := range handlers {
		h, ok := v.(*jsonx.Object)
		if !ok {
			return nil, errors.New("native hook handler must be an object")
		}
		raw, _ := h.Get("command")
		c, _ := raw.(string)
		if !strings.HasPrefix(c, commandPrefix) {
			kept = append(kept, h)
			continue
		}
		if e, ok := selected[name]; ok {
			kept = append(kept, handler(h, e, command))
			delete(selected, name)
		}
	}
	return kept, nil
}
