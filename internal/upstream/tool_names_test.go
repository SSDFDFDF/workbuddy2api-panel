package upstream

import (
	"strings"
	"testing"
)

func TestToolNameStateBounds(t *testing.T) {
	s := &toolNameState{declared: map[string]bool{strings.Repeat("a", maxToolNameCandidateBytes+1): true}}
	if err := s.add(strings.Repeat("a", maxToolNameCandidateBytes+1)); err == nil {
		t.Fatal("unbounded chunk")
	}
	names := map[string]bool{}
	for i := 1; i <= maxToolNameCandidates+1; i++ {
		names[strings.Repeat("a", i)] = true
	}
	s = &toolNameState{declared: names}
	// A stream of repeated "a" may represent the full name a, aa, aaa, ... .
	// Fail closed once retaining all alternatives exceeds the state bound.
	for i := 0; i < maxToolNameCandidates; i++ {
		if err := s.add("a"); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := s.add("a"); err == nil {
		t.Fatal("silently pruned name candidates")
	}
}

func FuzzToolNameResolution(f *testing.F) {
	f.Add("lookup|lookuplookup", "lookup|lookup")
	f.Add("lookup", "lo|look|up")
	f.Add("foo|foofoo", "foo|foo")
	f.Fuzz(func(t *testing.T, declarations, chunks string) {
		if len(declarations) > 256 || len(chunks) > 512 {
			t.Skip()
		}
		names := map[string]bool{}
		for _, name := range strings.Split(declarations, "|") {
			if name != "" {
				names[name] = true
			}
		}
		s := &toolNameState{declared: names}
		for _, chunk := range strings.Split(chunks, "|") {
			if err := s.add(chunk); err != nil {
				return
			}
		}
		name, err := s.name()
		count := 0
		for candidate := range s.prefixes {
			if names[candidate] {
				count++
			}
		}
		if err == nil && (count != 1 || !names[name] || !s.prefixes[name]) {
			t.Fatal("invented or ambiguous name", name)
		}
		if err != nil && count == 1 {
			t.Fatal("unique complete name rejected", err)
		}
	})
}
