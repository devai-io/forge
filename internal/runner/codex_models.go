package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// CodexModel is one model the machine's Codex offers, as its model picker
// lists it: the id `codex -m` takes, a display name, a line about it and the
// reasoning efforts it supports.
type CodexModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
}

// codexHome is $CODEX_HOME, else ~/.codex.
func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// codexModels reads the model list Codex keeps for this login
// (models_cache.json, refreshed by Codex itself whenever it runs): the
// models its picker shows, in its order. Nil when there is no cache (Codex
// never ran here, or a version without one) — the server then offers its
// own list.
func codexModels() []CodexModel {
	raw, err := os.ReadFile(filepath.Join(codexHome(), "models_cache.json"))
	if err != nil {
		return nil
	}
	var cache struct {
		Models []struct {
			Slug          string `json:"slug"`
			DisplayName   string `json:"display_name"`
			Description   string `json:"description"`
			Visibility    string `json:"visibility"`
			Priority      int    `json:"priority"`
			DefaultEffort string `json:"default_reasoning_level"`
			Efforts       []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &cache) != nil {
		return nil
	}
	sort.SliceStable(cache.Models, func(i, j int) bool { return cache.Models[i].Priority < cache.Models[j].Priority })
	var out []CodexModel
	for _, m := range cache.Models {
		if m.Slug == "" || !modelName.MatchString(m.Slug) || (m.Visibility != "" && m.Visibility != "list") {
			continue
		}
		cm := CodexModel{ID: m.Slug, Name: m.DisplayName, Description: m.Description, DefaultEffort: m.DefaultEffort}
		if cm.Name == "" {
			cm.Name = m.Slug
		}
		for _, e := range m.Efforts {
			if e.Effort != "" {
				cm.Efforts = append(cm.Efforts, e.Effort)
			}
		}
		out = append(out, cm)
		if len(out) == 30 {
			break
		}
	}
	return out
}
