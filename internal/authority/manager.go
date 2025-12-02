package authority

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gitopedia/researcher/internal/github"
	"github.com/gitopedia/researcher/internal/llm"
)

type EntityEntry struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Aliases []string `json:"aliases"`
}

type Manager struct {
	gh github.GitHubClient
	// maps type (person, org, etc) -> list of entries
	data    map[string][]EntityEntry
	updated map[string][]EntityEntry // track changes
	shas    map[string]string        // track original file SHAs
}

func NewManager(gh github.GitHubClient) *Manager {
	return &Manager{
		gh:      gh,
		data:    make(map[string][]EntityEntry),
		updated: make(map[string][]EntityEntry),
		shas:    make(map[string]string),
	}
}

func (m *Manager) Load(ref string) error {
	files := map[string]string{
		"person": "authority/people.json",
		"org":    "authority/orgs.json",
		"place":  "authority/places.json",
		"topic":  "authority/topics.json",
	}

	for typeKey, path := range files {
		content, sha, err := m.gh.GetFile(ref, path)
		if err != nil {
			slog.Warn("Could not load authority file, assuming empty", "path", path, "error", err)
			m.data[typeKey] = []EntityEntry{}
			continue
		}

		m.shas[typeKey] = sha
		var entries []EntityEntry
		if err := json.Unmarshal([]byte(content), &entries); err != nil {
			slog.Warn("Could not parse authority file, assuming empty", "path", path, "error", err)
			m.data[typeKey] = []EntityEntry{}
			continue
		}
		m.data[typeKey] = entries
	}
	return nil
}

func (m *Manager) ResolveEntities(entities []llm.ExtractedEntity) (map[string][]string, error) {
	// returns map of type -> list of IDs
	result := make(map[string][]string)

	for _, ent := range entities {
		typeKey := string(ent.Type)
		// normalize type key if needed (e.g. "people" vs "person")
		// LLM returns "person", "org", "place", "topic"
		// Files are keys in m.data matching these.

		entries, ok := m.data[typeKey]
		if !ok {
			// unknown type, skip or log
			continue
		}

		id := m.findEntity(entries, ent.Name)
		if id == "" {
			// Create new
			newID, newEntry := m.createEntity(typeKey, ent.Name)
			m.data[typeKey] = append(m.data[typeKey], newEntry)
			// Mark for update
			m.updated[typeKey] = m.data[typeKey]
			id = newID
			slog.Info("Created new authority", "name", ent.Name, "id", id)
		}
		result[typeKey] = append(result[typeKey], id)
	}

	return result, nil
}

func (m *Manager) findEntity(entries []EntityEntry, name string) string {
	normalizedName := strings.ToLower(strings.TrimSpace(name))
	for _, e := range entries {
		if strings.ToLower(e.Label) == normalizedName {
			return e.ID
		}
		for _, a := range e.Aliases {
			if strings.ToLower(a) == normalizedName {
				return e.ID
			}
		}
	}
	return ""
}

func (m *Manager) createEntity(typeKey, name string) (string, EntityEntry) {
	// ID format: type:slug (to ensure uniqueness and readability)
	// e.g. org:openai, person:albert-einstein
	slug := sanitizeSlug(name)
	// keep slug reasonable length (long enough to avoid collisions)
	if len(slug) > 200 {
		slug = slug[:200]
	}
	// Using a short suffix from ULID might be enough, or full ULID?
	// Roadmap examples: org:openai. If we want to avoid collisions automatically, appending ULID is safer.
	// But "org:openai" is nicer.
	// Let's use org:slug and check collision?
	// For MVP, let's use type:slug. If it exists, append random?
	// But we already checked `findEntity`. If we are here, the *Label* didn't match.
	// The *ID* might match if different label maps to same slug.
	// Let's iterate to find unique ID.

	baseID := fmt.Sprintf("%s:%s", typeKey, slug)
	id := baseID
	counter := 1
	for m.idExists(typeKey, id) {
		id = fmt.Sprintf("%s-%d", baseID, counter)
		counter++
	}

	entry := EntityEntry{
		ID:      id,
		Label:   name,
		Aliases: []string{},
	}
	return id, entry
}

func (m *Manager) idExists(typeKey, id string) bool {
	for _, e := range m.data[typeKey] {
		if e.ID == id {
			return true
		}
	}
	return false
}

// sanitizeSlug creates a YAML-safe slug from an entity name.
// It removes or replaces characters that would break YAML parsing when used in arrays.
// Examples:
//   - "Cathy O'Neil (author, \"Weapons of Math Destruction\")" -> "cathy-oneil"
//   - "John Smith (physicist)" -> "john-smith"
//   - "Apple Inc." -> "apple-inc"
func sanitizeSlug(name string) string {
	// First, extract the main name before any parenthetical qualifier
	// This handles cases like "John Smith (physicist)" -> "John Smith"
	mainName := name
	if idx := strings.Index(name, "("); idx > 0 {
		mainName = strings.TrimSpace(name[:idx])
	}

	// Convert to lowercase
	slug := strings.ToLower(mainName)

	// Replace spaces with hyphens
	slug = strings.ReplaceAll(slug, " ", "-")

	// Remove or replace problematic characters for YAML
	// These characters break YAML array syntax: " ' [ ] { } , : # & * ! | > < ` @ %
	var result strings.Builder
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
			result.WriteRune(r)
		case r >= '0' && r <= '9':
			result.WriteRune(r)
		case r == '-':
			result.WriteRune(r)
		case r == '.':
			result.WriteRune(r)
		// Skip all other characters (quotes, brackets, etc.)
		}
	}

	slug = result.String()

	// Clean up multiple consecutive hyphens
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}

	// Trim leading/trailing hyphens and dots
	slug = strings.Trim(slug, "-.")

	return slug
}

type FileUpdate struct {
	Content string
	SHA     string
}

func (m *Manager) GetUpdates() (map[string]FileUpdate, error) {
	updates := make(map[string]FileUpdate)
	files := map[string]string{
		"person": "authority/people.json",
		"org":    "authority/orgs.json",
		"place":  "authority/places.json",
		"topic":  "authority/topics.json",
	}

	for typeKey, entries := range m.updated {
		path := files[typeKey]
		bytes, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return nil, err
		}
		updates[path] = FileUpdate{
			Content: string(bytes),
			SHA:     m.shas[typeKey],
		}
	}
	return updates, nil
}
