package agent

import (
	"log"
	"sort"
	"time"
)

type perfArticle struct {
	counters  map[string]int
	durations map[string]time.Duration
}

func newPerfArticle() *perfArticle {
	return &perfArticle{
		counters:  make(map[string]int),
		durations: make(map[string]time.Duration),
	}
}

// PerfTracker collects per-article and per-run performance counters and timings.
// This is intended to make slow runs diagnosable from logs.
type PerfTracker struct {
	start time.Time

	bySlug map[string]*perfArticle

	urlCounts    map[string]int
	domainCounts map[string]int
}

func NewPerfTracker() *PerfTracker {
	return &PerfTracker{
		start:        time.Now(),
		bySlug:       make(map[string]*perfArticle),
		urlCounts:    make(map[string]int),
		domainCounts: make(map[string]int),
	}
}

func (p *PerfTracker) article(slug string) *perfArticle {
	if p == nil {
		return nil
	}
	a, ok := p.bySlug[slug]
	if !ok {
		a = newPerfArticle()
		p.bySlug[slug] = a
	}
	return a
}

func (p *PerfTracker) Inc(slug, key string, n int) {
	if p == nil {
		return
	}
	a := p.article(slug)
	a.counters[key] += n
}

func (p *PerfTracker) AddDur(slug, key string, d time.Duration) {
	if p == nil {
		return
	}
	a := p.article(slug)
	a.durations[key] += d
}

func (p *PerfTracker) RecordURL(canonURL, domain string) {
	if p == nil || canonURL == "" {
		return
	}
	p.urlCounts[canonURL]++
	if domain != "" {
		p.domainCounts[domain]++
	}
}

func (p *PerfTracker) LogAllArticles() {
	if p == nil {
		return
	}
	if len(p.bySlug) == 0 {
		return
	}
	slugs := make([]string, 0, len(p.bySlug))
	for slug := range p.bySlug {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		p.LogArticle(slug)
	}
}

func (p *PerfTracker) LogArticle(slug string) {
	if p == nil {
		return
	}
	a, ok := p.bySlug[slug]
	if !ok {
		return
	}
	// Only log if something happened.
	if len(a.counters) == 0 && len(a.durations) == 0 {
		return
	}

	get := func(k string) int { return a.counters[k] }
	dur := func(k string) time.Duration { return a.durations[k].Round(time.Millisecond) }

	log.Printf("[Perf][%s] searches=%d results_seen=%d skipped_used=%d skipped_attempted=%d fetch_ok=%d fetch_err=%d summarize_ok=%d summarize_err=%d rejected=%d relevant=%d | t_search=%s t_fetch=%s t_summarize=%s",
		slug,
		get("search.calls"),
		get("results.seen"),
		get("skip.used"),
		get("skip.attempted"),
		get("fetch.ok"),
		get("fetch.err"),
		get("summarize.ok"),
		get("summarize.err"),
		get("summarize.rejected"),
		get("summarize.relevant"),
		dur("search.time"),
		dur("fetch.time"),
		dur("summarize.time"),
	)
}

func (p *PerfTracker) LogRun() {
	if p == nil {
		return
	}
	runDur := time.Since(p.start).Round(time.Second)

	type kv struct {
		k string
		v int
	}
	var topURLs []kv
	for k, v := range p.urlCounts {
		if v >= 2 {
			topURLs = append(topURLs, kv{k: k, v: v})
		}
	}
	sort.Slice(topURLs, func(i, j int) bool {
		if topURLs[i].v == topURLs[j].v {
			return topURLs[i].k < topURLs[j].k
		}
		return topURLs[i].v > topURLs[j].v
	})
	if len(topURLs) > 20 {
		topURLs = topURLs[:20]
	}

	var topDomains []kv
	for k, v := range p.domainCounts {
		if v >= 2 {
			topDomains = append(topDomains, kv{k: k, v: v})
		}
	}
	sort.Slice(topDomains, func(i, j int) bool {
		if topDomains[i].v == topDomains[j].v {
			return topDomains[i].k < topDomains[j].k
		}
		return topDomains[i].v > topDomains[j].v
	})
	if len(topDomains) > 20 {
		topDomains = topDomains[:20]
	}

	log.Printf("[Perf][Run] duration=%s articles=%d unique_urls=%d", runDur, len(p.bySlug), len(p.urlCounts))
	if len(topDomains) > 0 {
		for _, d := range topDomains {
			log.Printf("[Perf][Run] hot_domain x%d: %s", d.v, d.k)
		}
	}
	if len(topURLs) > 0 {
		for _, u := range topURLs {
			log.Printf("[Perf][Run] hot_url x%d: %s", u.v, u.k)
		}
	}
}

