package engine

// prompt_cache.go — prompt cache 指纹(skill #09)。
// FNV-1a 哈希 + cache break 归因 + versioned 失效。

import (
	"fmt"
	"hash/fnv"
)

type PromptCacheFingerprint struct {
	PromptHash string
	Version    int
	CacheHit   bool
}

func ComputeFingerprint(prompt string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(prompt))
	return fmt.Sprintf("%016x", h.Sum64())
}

func DetectCacheBreak(prevHash, currHash string) bool {
	if prevHash == "" {
		return false
	}
	return prevHash != currHash
}

func CheckCache(prev PromptCacheFingerprint, prompt string, version int) PromptCacheFingerprint {
	currHash := ComputeFingerprint(prompt)
	hit := prev.PromptHash != "" &&
		prev.PromptHash == currHash &&
		prev.Version == version
	return PromptCacheFingerprint{
		PromptHash: currHash,
		Version:    version,
		CacheHit:   hit,
	}
}
