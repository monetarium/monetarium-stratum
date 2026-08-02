package mining

import (
	"bytes"
	"testing"
)

func TestWorkManagerSetCurrent(t *testing.T) {
	manager := NewWorkManager(10)
	data1 := makeWorkBlob(t, testHeader(t))
	data2 := makeWorkBlob(t, testHeader(t))
	data2[0] = ^data2[0]

	work1, clean1 := manager.SetCurrent(data1, ReasonNewTxns)
	if work1 == nil {
		t.Fatal("expected work to be set")
	}
	if work1.JobID() != "1" {
		t.Fatalf("job id got %q want 1", work1.JobID())
	}
	if clean1 {
		t.Fatal("expected clean=false for NewTxns")
	}

	// Duplicate data must not create a new job.
	work1dup, _ := manager.SetCurrent(data1, ReasonNewTxns)
	if work1dup != work1 {
		t.Fatal("duplicate data must return the same work")
	}
	if manager.Current() != work1 {
		t.Fatal("current work mismatch")
	}

	// New data creates a new job; NewParent marks it clean.
	work2, clean2 := manager.SetCurrent(data2, ReasonNewParent)
	if work2 == nil || work2 == work1 {
		t.Fatal("expected distinct new work")
	}
	if work2.JobID() != "2" {
		t.Fatalf("job id got %q want 2", work2.JobID())
	}
	if !clean2 {
		t.Fatal("expected clean=true for NewParent")
	}

	// Both jobs remain lookup-able.
	if _, ok := manager.Job("1"); !ok {
		t.Fatal("job 1 should exist")
	}
	if _, ok := manager.Job("2"); !ok {
		t.Fatal("job 2 should exist")
	}
	if _, ok := manager.Job("nope"); ok {
		t.Fatal("unknown job should not exist")
	}
}

func TestWorkManagerEviction(t *testing.T) {
	manager := NewWorkManager(3)
	base := makeWorkBlob(t, testHeader(t))
	var data []byte
	for i := 0; i < 5; i++ {
		data = make([]byte, len(base))
		copy(data, base)
		data[4] = byte(i)
		manager.SetCurrent(data, ReasonNewTxns)
	}

	// Only the last 3 jobs may be retained.
	for _, id := range []string{"1", "2"} {
		if _, ok := manager.Job(id); ok {
			t.Fatalf("job %s should have been evicted", id)
		}
	}
	for _, id := range []string{"3", "4", "5"} {
		if _, ok := manager.Job(id); !ok {
			t.Fatalf("job %s should be retained", id)
		}
	}
}

func TestWorkManagerDuplicateDataBytes(t *testing.T) {
	manager := NewWorkManager(10)
	data := makeWorkBlob(t, testHeader(t))
	manager.SetCurrent(data, ReasonNewTxns)

	// A copy with identical bytes must also be deduplicated.
	dup := make([]byte, len(data))
	copy(dup, data)
	work, _ := manager.SetCurrent(dup, ReasonNewTxns)
	if !bytes.Equal(work.Data(), data) {
		t.Fatal("deduplicated work data mismatch")
	}
}
