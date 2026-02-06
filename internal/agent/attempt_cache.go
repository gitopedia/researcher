package agent

// AttemptCache is a lightweight in-memory cache of canonical URLs attempted per article slug.
// It is reset per topic run and complements persisted `sources_attempted` metadata, preventing
// repeated work before metadata is saved back to the repo.
type AttemptCache struct {
	bySlug map[string]map[string]bool // slug -> canonicalURL -> attempted
}

func NewAttemptCache() *AttemptCache {
	return &AttemptCache{
		bySlug: make(map[string]map[string]bool),
	}
}

func (c *AttemptCache) ensureSlug(slug string) map[string]bool {
	if c.bySlug == nil {
		c.bySlug = make(map[string]map[string]bool)
	}
	m, ok := c.bySlug[slug]
	if !ok {
		m = make(map[string]bool)
		c.bySlug[slug] = m
	}
	return m
}

// SeedFromMeta seeds the cache from current metadata. Safe to call repeatedly.
func (c *AttemptCache) SeedFromMeta(slug string, meta *ArticleMetadata) {
	if c == nil || meta == nil {
		return
	}
	m := c.ensureSlug(slug)
	for _, s := range meta.SourcesUsed {
		if s.CanonicalURL != "" {
			m[s.CanonicalURL] = true
			continue
		}
		if canon := canonicalizeURL(s.URL); canon != "" {
			m[canon] = true
		}
	}
	for _, a := range meta.SourcesAttempted {
		if a.CanonicalURL != "" {
			m[a.CanonicalURL] = true
		}
	}
}

func (c *AttemptCache) IsAttempted(slug, canonicalURL string) bool {
	if c == nil || canonicalURL == "" {
		return false
	}
	if c.bySlug == nil {
		return false
	}
	m, ok := c.bySlug[slug]
	if !ok {
		return false
	}
	return m[canonicalURL]
}

func (c *AttemptCache) MarkAttempted(slug, canonicalURL string) {
	if c == nil || canonicalURL == "" {
		return
	}
	m := c.ensureSlug(slug)
	m[canonicalURL] = true
}

