// cache.go provides a process metadata cache keyed by (PID, createTime) to
// avoid re-reading /proc on every discovery cycle. Entries are pruned after
// 20 minutes of staleness.
package discovery

import (
	"fmt"
	"sync"
	"time"
)

type ProcessCacheEntry struct {
	ServiceName       string
	ServiceType       string
	RuntimeVersion    string
	EntryPoint        string
	HasAgent          bool
	IsMiddlewareAgent bool
	AgentPath         string
	AgentType         string // "middleware", "opentelemetry", "otel-injector", etc.
	Owner             string
	ContainerInfo     *ContainerInfo
	Ignore            bool
	LastSeen          int64

	SystemdUnit         string
	ExplicitServiceName string
	WorkingDirectory    string
	PackageName         string
	ModulePath          string
}

type ProcessMetadataCache struct {
	data map[string]ProcessCacheEntry
	mu   sync.Mutex
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

	globalProcessCache.mu.Lock()
	defer globalProcessCache.mu.Unlock()

	val, ok := globalProcessCache.data[key]
	if ok {
		val.LastSeen = time.Now().Unix()
		globalProcessCache.data[key] = val
		return val, true
	}

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

