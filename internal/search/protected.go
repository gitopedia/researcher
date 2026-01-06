package search

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

const (
	// ProtectionThreshold is the number of timeouts before a domain is marked as protected
	ProtectionThreshold = 5
	// ProtectionExpiryDays is how long a domain stays protected before being re-evaluated
	ProtectionExpiryDays = 30
)

// DomainRecord tracks timeout/protection information for a single domain
type DomainRecord struct {
	TimeoutCount int        `json:"timeout_count"`
	FirstSeen    time.Time  `json:"first_seen"`
	LastSeen     time.Time  `json:"last_seen"`
	ProtectedAt  *time.Time `json:"protected_at,omitempty"`
}

// ProtectedDomainsDelta represents new domain records to be exported
type ProtectedDomainsDelta struct {
	ExportedAt        time.Time                `json:"exported_at"`
	ResearcherVersion string                   `json:"researcher_version"`
	Domains           map[string]*DomainRecord `json:"domains"`
}

// ProtectedDomains manages the list of domains that are known to be protected
type ProtectedDomains struct {
	Domains  map[string]*DomainRecord `json:"domains"`
	filePath string
	mu       sync.RWMutex

	// Delta tracking: domains added or updated during this session
	sessionDomains map[string]*DomainRecord
}

// NewProtectedDomains creates a new empty ProtectedDomains instance
func NewProtectedDomains(filePath string) *ProtectedDomains {
	return &ProtectedDomains{
		Domains:        make(map[string]*DomainRecord),
		filePath:       filePath,
		sessionDomains: make(map[string]*DomainRecord),
	}
}

// LoadProtectedDomains loads the protected domains list from a file
func LoadProtectedDomains(filePath string) (*ProtectedDomains, error) {
	pd := NewProtectedDomains(filePath)

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, that's fine
			return pd, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, pd); err != nil {
		return nil, err
	}

	// Ensure sessionDomains is initialized after unmarshal
	if pd.sessionDomains == nil {
		pd.sessionDomains = make(map[string]*DomainRecord)
	}

	// Clean expired entries on load
	pd.cleanExpired()

	return pd, nil
}

// Save persists the protected domains list to the file
func (pd *ProtectedDomains) Save() error {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	// Clean expired entries before saving
	pd.cleanExpiredLocked()

	data, err := json.MarshalIndent(pd, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(pd.filePath, data, 0644)
}

// IsProtected checks if a domain is currently protected (and not expired)
func (pd *ProtectedDomains) IsProtected(domain string) bool {
	pd.mu.RLock()
	defer pd.mu.RUnlock()

	record, exists := pd.Domains[domain]
	if !exists {
		return false
	}

	if record.ProtectedAt == nil {
		return false
	}

	// Check if protection has expired
	expiryTime := record.ProtectedAt.Add(ProtectionExpiryDays * 24 * time.Hour)
	if time.Now().After(expiryTime) {
		return false
	}

	return true
}

// RecordTimeout records a timeout for a domain and potentially marks it as protected
func (pd *ProtectedDomains) RecordTimeout(domain string) {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	now := time.Now()

	record, exists := pd.Domains[domain]
	if !exists {
		record = &DomainRecord{
			FirstSeen: now,
		}
		pd.Domains[domain] = record
	}

	// If already protected and not expired, don't update
	if record.ProtectedAt != nil {
		expiryTime := record.ProtectedAt.Add(ProtectionExpiryDays * 24 * time.Hour)
		if time.Now().Before(expiryTime) {
			return
		}
		// Protection expired, reset the counter
		record.TimeoutCount = 0
		record.ProtectedAt = nil
	}

	record.TimeoutCount++
	record.LastSeen = now

	// Check if we've hit the threshold
	if record.TimeoutCount >= ProtectionThreshold {
		record.ProtectedAt = &now
		log.Printf("Domain %s marked as protected after %d timeouts", domain, record.TimeoutCount)
	}

	// Track this domain in the session delta (copy the record)
	pd.sessionDomains[domain] = &DomainRecord{
		TimeoutCount: record.TimeoutCount,
		FirstSeen:    record.FirstSeen,
		LastSeen:     record.LastSeen,
		ProtectedAt:  record.ProtectedAt,
	}

	// Save after each update
	go func() {
		if err := pd.Save(); err != nil {
			log.Printf("Warning: failed to save protected domains: %v", err)
		}
	}()
}

// GetTimeoutCount returns the current timeout count for a domain
func (pd *ProtectedDomains) GetTimeoutCount(domain string) int {
	pd.mu.RLock()
	defer pd.mu.RUnlock()

	record, exists := pd.Domains[domain]
	if !exists {
		return 0
	}
	return record.TimeoutCount
}

// cleanExpired removes expired protection entries (called without lock)
func (pd *ProtectedDomains) cleanExpired() {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	pd.cleanExpiredLocked()
}

// cleanExpiredLocked removes expired entries (must be called with lock held)
func (pd *ProtectedDomains) cleanExpiredLocked() {
	now := time.Now()
	for domain, record := range pd.Domains {
		if record.ProtectedAt != nil {
			expiryTime := record.ProtectedAt.Add(ProtectionExpiryDays * 24 * time.Hour)
			if now.After(expiryTime) {
				// Reset the record instead of deleting (keep history)
				record.TimeoutCount = 0
				record.ProtectedAt = nil
				log.Printf("Protection expired for domain %s", domain)
			}
		}
	}
}

// Stats returns statistics about the protected domains
func (pd *ProtectedDomains) Stats() (total int, protected int) {
	pd.mu.RLock()
	defer pd.mu.RUnlock()

	total = len(pd.Domains)
	for _, record := range pd.Domains {
		if record.ProtectedAt != nil {
			expiryTime := record.ProtectedAt.Add(ProtectionExpiryDays * 24 * time.Hour)
			if time.Now().Before(expiryTime) {
				protected++
			}
		}
	}
	return
}

// HasDelta returns true if there are domains added/updated during this session
func (pd *ProtectedDomains) HasDelta() bool {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	return len(pd.sessionDomains) > 0
}

// GetDeltaCount returns the number of domains in the current session delta
func (pd *ProtectedDomains) GetDeltaCount() int {
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	return len(pd.sessionDomains)
}

// ExportDelta generates a delta file containing only domains added/updated during this session.
// Returns the filename and content, or empty strings if no delta exists.
// The filename is uniquely named with a timestamp.
func (pd *ProtectedDomains) ExportDelta(researcherVersion string) (filename string, content string, err error) {
	pd.mu.RLock()
	defer pd.mu.RUnlock()

	if len(pd.sessionDomains) == 0 {
		return "", "", nil
	}

	now := time.Now()
	delta := ProtectedDomainsDelta{
		ExportedAt:        now,
		ResearcherVersion: researcherVersion,
		Domains:           make(map[string]*DomainRecord),
	}

	// Copy session domains to delta
	for domain, record := range pd.sessionDomains {
		delta.Domains[domain] = &DomainRecord{
			TimeoutCount: record.TimeoutCount,
			FirstSeen:    record.FirstSeen,
			LastSeen:     record.LastSeen,
			ProtectedAt:  record.ProtectedAt,
		}
	}

	data, err := json.MarshalIndent(delta, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal delta: %w", err)
	}

	// Generate unique filename with timestamp
	filename = fmt.Sprintf("protected-domains-delta-%s.json", now.Format("20060102-150405"))

	return filename, string(data), nil
}

// ClearDelta clears the session delta tracking
func (pd *ProtectedDomains) ClearDelta() {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	pd.sessionDomains = make(map[string]*DomainRecord)
}




