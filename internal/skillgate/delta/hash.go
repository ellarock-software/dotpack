package delta

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// HashInfo is the content tripwire for one package.
type HashInfo struct {
	Digest              string `json:"digest"`
	HashedFiles         int    `json:"hashed_files"`
	RuntimeFilesSkipped int    `json:"runtime_files_skipped"`
}

// runtimeExclusions returns the directory names excluded from the
// content hash for this package.
//
// Nothing is excluded by default. A package earns an exclusion only by
// DECLARING the name in its own .gitignore AND that name being present
// in the policy allowlist. Granting the fixed allowlist unconditionally
// meant any package that simply shipped no .gitignore got logs/,
// node_modules/ and .venv/ excluded from the tripwire for free -- and
// the package controls whether that file exists, so that was a free
// hiding place rather than a safe default.
//
// The .gitignore is package-controlled, so it may only NARROW the
// allowlist, never extend it. Otherwise a package lists SKILL.md,
// excludes its own source from the hash, and a complete rewrite reports
// "content identical to approved baseline".
func runtimeExclusions(pkgAbs string, p Policy) set {
	if !p.HashExclusions.HonorPackageGitignore {
		return set{}
	}
	raw, err := os.ReadFile(filepath.Join(pkgAbs, ".gitignore"))
	if err != nil {
		// No .gitignore means no exclusions: everything is hashed.
		return set{}
	}

	declared := set{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		rooted := strings.HasPrefix(line, "/")
		cleaned := strings.TrimPrefix(line, "./")
		cleaned = strings.TrimLeft(cleaned, "/")
		cleaned = strings.TrimRight(cleaned, "/")
		if cleaned == "" || strings.Contains(cleaned, "*") {
			continue
		}
		if rooted {
			declared["/"+cleaned] = struct{}{}
		} else {
			declared[cleaned] = struct{}{}
		}
	}

	out := set{}
	for _, name := range p.HashExclusions.Paths {
		if declared.has(name) || declared.has("/"+name) {
			out[name] = struct{}{}
		}
	}
	return out
}

// isExcludedFromHash matches an exclusion against a package-relative
// path.
//
// Matching is root-anchored: declaring "logs" must not also exclude
// "scripts/logs/", which would hand a package a hiding place one
// directory deeper than it declared. Only names the policy marks
// nestable -- __pycache__ and friends, which genuinely recur below the
// root -- match at any depth.
func isExcludedFromHash(relPath string, exclusions, nestable set) bool {
	segs := strings.Split(relPath, string(filepath.Separator))
	if len(segs) == 0 {
		return false
	}
	if exclusions.has(segs[0]) {
		return true
	}
	for _, seg := range segs {
		if exclusions.has(seg) && nestable.has(seg) {
			return true
		}
	}
	return false
}

// packageHash is the content tripwire over the durable files of a
// package: every file the walk found, minus the runtime paths the
// package earned an exclusion for.
//
// Excluded paths are excluded from the HASH ONLY. They are still handed
// to the detector and still searched for hidden codepoints, so a payload
// dropped into logs/ is still caught -- the exclusion buys a stable
// tripwire for a skill that writes its own runtime state, not a blind
// spot.
//
// files must be in walk order; the relative path is folded into the
// digest before each file's own hash, so both the set of files and their
// order are covered.
func packageHash(pkgAbs string, files []string, p Policy) HashInfo {
	exclusions := runtimeExclusions(pkgAbs, p)
	nestable := newSet(p.HashExclusions.NestablePaths)

	h := sha256.New()
	info := HashInfo{}
	for _, abs := range files {
		rel, err := filepath.Rel(pkgAbs, abs)
		if err != nil {
			rel = abs
		}
		if isExcludedFromHash(rel, exclusions, nestable) {
			info.RuntimeFilesSkipped++
			continue
		}
		h.Write([]byte(rel))
		content, err := os.ReadFile(abs)
		if err != nil {
			// Counted, not skipped: an unreadable file is still part of
			// the package, and its becoming readable must move the hash.
			h.Write([]byte("UNREADABLE"))
		} else {
			sum := sha256.Sum256(content)
			h.Write([]byte(hex.EncodeToString(sum[:])))
		}
		info.HashedFiles++
	}
	info.Digest = hex.EncodeToString(h.Sum(nil))
	return info
}
