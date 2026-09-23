// Package store persists cache entries.
package store

// Entry is one cache entry.
type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	TTL   int    `json:"ttl"`
}
