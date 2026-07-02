package engine

// contextual_fragment.go — 结构化上下文片段库(skill #22)。
// 每片段独立 struct + 硬上限 <10K tokens + Immutable 禁止 rewrite。

import (
	"fmt"
	"sort"
	"strings"
)

const fragmentHardTokenCap = 10000

type ContextualFragment struct {
	ID         string
	Content    string
	TokenCount int
	Immutable  bool
	Priority   int
}

type FragmentStore struct {
	fragments map[string]*ContextualFragment
}

func NewFragmentStore() *FragmentStore {
	return &FragmentStore{fragments: make(map[string]*ContextualFragment)}
}

func (s *FragmentStore) Add(f ContextualFragment) bool {
	if f.TokenCount == 0 {
		f.TokenCount = CountTokens(f.Content)
	}
	if old, ok := s.fragments[f.ID]; ok && old.Immutable {
		return false
	}
	s.fragments[f.ID] = &f
	return true
}

func (s *FragmentStore) Get(id string) (*ContextualFragment, bool) {
	f, ok := s.fragments[id]
	return f, ok
}

func (s *FragmentStore) Render(maxTokens int) string {
	if maxTokens <= 0 || maxTokens > fragmentHardTokenCap {
		maxTokens = fragmentHardTokenCap
	}
	ordered := make([]*ContextualFragment, 0, len(s.fragments))
	for _, f := range s.fragments {
		ordered = append(ordered, f)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Immutable != ordered[j].Immutable {
			return ordered[i].Immutable
		}
		return ordered[i].Priority > ordered[j].Priority
	})
	var sb strings.Builder
	used := 0
	for _, f := range ordered {
		if used+f.TokenCount > maxTokens {
			continue
		}
		sb.WriteString(fmt.Sprintf("## %s\n%s\n\n", f.ID, f.Content))
		used += f.TokenCount
	}
	return sb.String()
}
