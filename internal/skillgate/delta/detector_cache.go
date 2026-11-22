package delta

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/ellarock-software/dotpack/internal/skillscanner"
)

const detectorCacheVersion = 1

// Only raw successful detector observations belong here. Occurrences,
// invisible-character findings, baseline state and verdicts are always live.
type detectorCacheEntry struct {
	SchemaVersion int       `json:"schema_version"`
	Findings      []Finding `json:"findings"`
}

func (g *Gate) cachedDetector(ctx context.Context, rt skillscanner.Runtime, pkgAbs string, walked []string) detectorResult {
	scan := func() detectorResult {
		return scanDetector(ctx, rt.ScannerBin, pkgAbs, skillscanner.DefaultScanTimeout)
	}
	scannerDigest, ok := regularFileDigest(rt.ScannerBin)
	if !ok || ctx.Err() != nil {
		return scan()
	}
	fullTreeDigest, ok := fullPackageDigest(pkgAbs, walked)
	if !ok {
		return scan()
	}
	sum := sha256.Sum256([]byte("dotpack-skillgate-detector-cache-v1\x00" +
		rt.Metadata.DetectorVersion() + "\x00" + scannerDigest + "\x00" +
		policy.PolicyVersion + "\x00" + fullTreeDigest + "\n"))
	path := filepath.Join(g.dirs.DotpackHome, "skillgate", "detector-cache", hex.EncodeToString(sum[:])+".json")
	if raw, err := os.ReadFile(path); err == nil {
		var entry detectorCacheEntry
		if err := json.Unmarshal(raw, &entry); err == nil && entry.SchemaVersion == detectorCacheVersion && entry.Findings != nil {
			return detectorResult{Findings: entry.Findings}
		}
	}

	det := scan()
	if det.Err != "" {
		return det
	}
	// Remove checkout-specific evidence before either returning or storing
	// findings. withOccurrences must see the same input on misses and hits.
	det.Findings = slices.Clone(det.Findings)
	for i := range det.Findings {
		f := &det.Findings[i]
		f.File = normalisePath(f.File, pkgAbs)
		f.Why = normalisePath(f.Why, pkgAbs)
		f.Snippet = normalisePath(f.Snippet, pkgAbs)
	}
	// Only persist observations while the package and scanner still match
	// the inputs used for the cache key. Re-walk to catch added files or links.
	afterWalk, err := walkPackage(pkgAbs)
	afterDigest, afterOK := fullPackageDigest(pkgAbs, afterWalk.Files)
	if err == nil && len(afterWalk.Links) == 0 && afterOK && afterDigest == fullTreeDigest {
		if scannerAfter, sOK := regularFileDigest(rt.ScannerBin); sOK && scannerAfter == scannerDigest {
			writeDetectorCache(path, detectorCacheEntry{SchemaVersion: detectorCacheVersion, Findings: det.Findings})
		}
	}
	return det
}

// Cache persistence is best effort. Readers only see complete JSON after an
// atomic rename; every read/decode failure falls back to the live detector.
func writeDetectorCache(path string, entry detectorCacheEntry) {
	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.CreateTemp(dir, ".detector-cache-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, writeErr := f.Write(raw)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		return
	}
	_ = os.Rename(f.Name(), path)
}
