package delta

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// walkResult is one package enumeration.
type walkResult struct {
	// Files are the regular files, absolute, in traversal order. That
	// order is part of the content hash, so it must not be re-sorted.
	Files []string

	// Links are symlinks. They are COLLECTED, never followed: a
	// symlinked SKILL.md is hashed by nothing and scanned by nothing --
	// the detector does not follow them either -- so a package could be
	// approved with zero bytes read and its targets swapped afterwards.
	Links []string
}

// walkPackage enumerates pkgAbs.
//
// filepath.WalkDir gives exactly the traversal the source implementation
// produced: pre-order, entries in lexical order, directories interleaved
// with files rather than visited after them. A hand-rolled "files first,
// then recurse" walk would produce a different content hash for every
// multi-directory package.
//
// Unlike the source, an unreadable directory is an error rather than a
// silently truncated listing. Returning partial results there means part
// of a tree can be approved having never been inspected.
func walkPackage(pkgAbs string) (walkResult, error) {
	var out walkResult
	err := filepath.WalkDir(pkgAbs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if path == pkgAbs {
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			// Not descended even when it points at a directory, so a
			// symlinked subtree cannot smuggle content past the hash.
			out.Links = append(out.Links, path)
			return nil
		case d.IsDir():
			return nil
		case d.Type().IsRegular():
			// Every regular file is walked, including .DS_Store. Dropping
			// it here would have made it a channel that is neither hashed
			// nor scanned -- while install copies it verbatim.
			out.Files = append(out.Files, path)
			return nil
		default:
			// Sockets, fifos and devices are not skill content.
			return nil
		}
	})
	if err != nil {
		return walkResult{}, err
	}
	return out, nil
}

// unsafeLink is a symlink that fails the gate.
type unsafeLink struct {
	File   string `json:"file"`
	Target string `json:"target"`
}

// findSymlinks returns the links that are not safe.
//
// A link resolving inside the package whose target exists is permitted:
// npm's node_modules/.bin entries are the common case, and such a link
// cannot be repointed at content outside the hashed and scanned tree. A
// link that escapes the package, dangles, or cannot be read is an
// approval bypass and fails closed.
func findSymlinks(pkgAbs string, links []string) []unsafeLink {
	var unsafe []unsafeLink
	for _, link := range links {
		raw, err := os.Readlink(link)
		if err != nil {
			unsafe = append(unsafe, unsafeLink{File: relOrPath(pkgAbs, link), Target: "<unreadable>"})
			continue
		}
		resolved := raw
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(link), raw)
		}
		resolved = filepath.Clean(resolved)

		inside := resolved == pkgAbs || strings.HasPrefix(resolved, pkgAbs+string(filepath.Separator))
		if !inside {
			unsafe = append(unsafe, unsafeLink{File: relOrPath(pkgAbs, link), Target: raw})
			continue
		}
		// A dangling link inside the package is still unsafe: its target
		// can be created after approval.
		if _, err := os.Stat(resolved); err != nil {
			unsafe = append(unsafe, unsafeLink{File: relOrPath(pkgAbs, link), Target: raw})
		}
	}
	return unsafe
}

func relOrPath(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}
