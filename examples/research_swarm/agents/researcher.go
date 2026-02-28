package agents

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// Finding captures one agent's research output.
type Finding struct {
	Agent   string
	Source  string
	Summary string
	Trends  []string
}

// ResearchAgent defines behavior for one research worker in the swarm.
type ResearchAgent interface {
	Name() string
	Run(ctx context.Context, query string) (Finding, error)
}

// ResultStore keeps findings safe for concurrent writes.
type ResultStore struct {
	mu       sync.Mutex
	findings []Finding
}

// Add stores one finding.
func (s *ResultStore) Add(f Finding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.findings = append(s.findings, f)
}

// Snapshot returns a copy of all findings.
func (s *ResultStore) Snapshot() []Finding {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Finding, len(s.findings))
	copy(out, s.findings)
	return out
}

// SynthesizeTopTrends aggregates all findings into a top-k trend list.
func SynthesizeTopTrends(findings []Finding, topK int) []string {
	if topK <= 0 {
		return nil
	}

	freq := make(map[string]int)
	for _, f := range findings {
		for _, trend := range f.Trends {
			normalized := strings.TrimSpace(strings.ToLower(trend))
			if normalized == "" {
				continue
			}
			freq[normalized]++
		}
	}

	type pair struct {
		trend string
		count int
	}
	pairs := make([]pair, 0, len(freq))
	for trend, count := range freq {
		pairs = append(pairs, pair{trend: trend, count: count})
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].trend < pairs[j].trend
		}
		return pairs[i].count > pairs[j].count
	})

	if len(pairs) < topK {
		topK = len(pairs)
	}
	out := make([]string, 0, topK)
	for i := 0; i < topK; i++ {
		out = append(out, pairs[i].trend)
	}
	return out
}
