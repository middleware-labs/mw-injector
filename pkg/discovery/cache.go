package discovery

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time" // Added import
)

type ProcessCacheEntry struct {
	ServiceName       string
	ServiceType       string
	RuntimeVersion    string
	EntryPoint        string
	HasAgent          bool
	IsMiddlewareAgent bool
	AgentPath         string
	Owner             string
	ContainerInfo     *ContainerInfo
	Ignore            bool
	LastSeen          int64
}

type ProcessMetadataCache struct {
	data   map[string]ProcessCacheEntry
	mu     sync.RWMutex
	hits   int64
	misses int64
}

var (
	globalProcessCache = ProcessMetadataCache{
		data: make(map[string]ProcessCacheEntry),
	}
)

func makeProcessKey(pid int32, createTime int64) string {
	return fmt.Sprintf("%d-%d", pid, createTime)
}

func GetCachedProcessMetadata(pid int32, createTime int64) (ProcessCacheEntry, bool) {
	key := makeProcessKey(pid, createTime)

	// CHANGE: We need a Write Lock now because we are updating LastSeen
	globalProcessCache.mu.Lock()
	defer globalProcessCache.mu.Unlock()

	val, ok := globalProcessCache.data[key]
	if ok {
		// HIT: Update the timestamp so it stays alive
		val.LastSeen = time.Now().Unix()
		globalProcessCache.data[key] = val

		atomic.AddInt64(&globalProcessCache.hits, 1)
		return val, true
	}

	atomic.AddInt64(&globalProcessCache.misses, 1)
	return ProcessCacheEntry{}, false
}

func CacheProcessMetadata(pid int32, createTime int64, entry ProcessCacheEntry) {
	key := makeProcessKey(pid, createTime)

	globalProcessCache.mu.Lock()
	defer globalProcessCache.mu.Unlock()

	// Set the timestamp on creation
	entry.LastSeen = time.Now().Unix()
	globalProcessCache.data[key] = entry
}

// PruneProcessCache deletes anything older than 30 minutes
func PruneProcessCache() {
	globalProcessCache.mu.Lock()
	defer globalProcessCache.mu.Unlock()

	threshold := time.Now().Add(-20 * time.Minute).Unix()

	for key, entry := range globalProcessCache.data {
		if entry.LastSeen < threshold {
			delete(globalProcessCache.data, key)
		}
	}
}

// --- Stats Methods ---

// ReportCacheStats returns a human-readable string of current cache performance.
// You can log this at the end of GetAgentReportValue()
func ReportCacheStats() string {
	hits := atomic.LoadInt64(&globalProcessCache.hits)
	misses := atomic.LoadInt64(&globalProcessCache.misses)
	total := hits + misses

	if total == 0 {
		return "Cache Stats: No lookups yet"
	}

	rate := float64(hits) / float64(total) * 100
	return fmt.Sprintf("Discovery Cache: %d Hits, %d Misses (%.1f%% Hit Rate), %d Items Cached",
		hits, misses, rate, countItems())
}

// ResetCacheStats clears the counters (useful if you want stats per-run)
func ResetCacheStats() {
	atomic.StoreInt64(&globalProcessCache.hits, 0)
	atomic.StoreInt64(&globalProcessCache.misses, 0)
}

// Helper to count items thread-safely
func countItems() int {
	globalProcessCache.mu.RLock()
	defer globalProcessCache.mu.RUnlock()
	return len(globalProcessCache.data)
}
