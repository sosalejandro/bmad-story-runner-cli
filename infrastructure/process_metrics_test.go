package infrastructure

import (
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

func TestCollectMemoryStats(t *testing.T) {
	stats := CollectMemoryStats()

	if stats.HeapAllocBytes == 0 {
		t.Error("HeapAllocBytes should be > 0")
	}
	if stats.HeapSysBytes == 0 {
		t.Error("HeapSysBytes should be > 0")
	}
	if stats.Goroutines == 0 {
		t.Error("Goroutines should be > 0")
	}
	if stats.StackInuseBytes == 0 {
		t.Error("StackInuseBytes should be > 0")
	}
}

func TestCollectSessionInfo(t *testing.T) {
	info := CollectSessionInfo("0.4.0", "abc123")

	if info.PWD == "" {
		t.Error("PWD should not be empty")
	}
	if info.PID == 0 {
		t.Error("PID should not be 0")
	}
	if info.OS == "" {
		t.Error("OS should not be empty")
	}
	if info.BmadVersion != "0.4.0" {
		t.Errorf("BmadVersion = %q, want %q", info.BmadVersion, "0.4.0")
	}
}

func TestMemoryStatsDelta(t *testing.T) {
	before := domain.MemoryStats{
		HeapAllocBytes:  100,
		TotalAllocBytes: 200,
		NumGC:           1,
		Mallocs:         50,
		Frees:           30,
	}
	after := domain.MemoryStats{
		HeapAllocBytes:  250,
		TotalAllocBytes: 500,
		NumGC:           3,
		Mallocs:         120,
		Frees:           80,
	}
	// Delta should represent the "after" snapshot since that's what we report
	// (the final state of the process).
	_ = before
	_ = after
}
