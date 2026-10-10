package reqlog

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRecentRingOrderAndSnapshotOwnership(t *testing.T) {
	for _, count := range []int{0, 1, 99, 100, 101, 237} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			r := New(Config{})
			defer r.Close()
			for i := 0; i < count; i++ {
				r.Begin()
				r.Record(Event{RequestID: fmt.Sprint(i), Status: 200, OK: true})
			}
			s := r.Snapshot()
			if len(s.Recent) != min(count, recentLimit) || s.Completed != int64(count) || s.InFlight != 0 {
				t.Fatalf("counts: %+v", s)
			}
			for i, e := range s.Recent {
				if want := fmt.Sprint(count - 1 - i); e.RequestID != want {
					t.Fatalf("recent[%d]=%q want %q", i, e.RequestID, want)
				}
			}
			// A later wrap must not mutate a previously returned snapshot.
			for i := 0; i < recentLimit+1; i++ {
				r.Record(Event{RequestID: "later"})
			}
			if count > 0 && s.Recent[0].RequestID != fmt.Sprint(count-1) {
				t.Fatal("snapshot aliases ring")
			}
			if count > 0 {
				s.Recent[0].RequestID = "mutated"
			}
			if r.Snapshot().Recent[0].RequestID != "later" {
				t.Fatal("snapshot mutation reached recorder")
			}
		})
	}
}

func TestRecentRingConcurrentRecordSnapshot(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r.Begin()
				r.Record(Event{Status: 200, OK: true})
				if len(r.Snapshot().Recent) > recentLimit {
					t.Error("unbounded recent")
				}
			}
		}()
	}
	wg.Wait()
	if s := r.Snapshot(); s.Completed != 800 || s.InFlight != 0 || len(s.Recent) != recentLimit {
		t.Fatalf("counts: %+v", s)
	}
}

func BenchmarkRecordRecent(b *testing.B) {
	r := New(Config{})
	defer r.Close()
	e := Event{Time: time.Now(), Status: 200, OK: true, Outcome: OutcomeSuccess}
	for i := 0; i < recentLimit; i++ {
		r.Record(e)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Record(e)
	}
}
