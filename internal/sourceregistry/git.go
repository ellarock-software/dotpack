package sourceregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ellarock-software/dotpack/internal/dirs"
)

var (
	githubIdentRE = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	commitSHARE   = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

// GitRunner defines the execution signature for git commands.
type GitRunner func(ctx context.Context, workDir string, args ...string) ([]byte, error)

// DefaultGitRunner executes git with safe defaults and isolation.
var DefaultGitRunner GitRunner = func(ctx context.Context, workDir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// CurrentGitRunner allows overriding git execution in tests.
var CurrentGitRunner = DefaultGitRunner

// ParsedSource represents the normalized components of a repository source spec.
type ParsedSource struct {
	ID       string // Normalized ID, e.g. "owner/repo"
	CloneURL string // "https://github.com/owner/repo.git"
	Ref      string // requested ref, empty if following default branch
	Owner    string
	Repo     string
}

// ParseSourceSpec parses and normalizes a repository input.
func ParseSourceSpec(input string) (ParsedSource, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return ParsedSource{}, fmt.Errorf("empty repository source")
	}

	var spec, ref string

	if strings.HasPrefix(input, "github:") {
		spec = strings.TrimPrefix(input, "github:")
	} else if strings.HasPrefix(input, "https://github.com/") || strings.HasPrefix(input, "http://github.com/") {
		parsed, err := url.Parse(input)
		if err != nil {
			return ParsedSource{}, fmt.Errorf("parse github url %q: %w", input, err)
		}
		spec = strings.Trim(parsed.Path, "/")
		if parsed.Fragment != "" {
			ref = parsed.Fragment
		}
	} else if strings.HasPrefix(input, "file://") {
		spec = strings.TrimPrefix(input, "file://")
	} else {
		spec = input
	}

	if ref == "" && strings.Contains(spec, "@") {
		main, r, _ := strings.Cut(spec, "@")
		spec = main
		ref = r
	} else if ref == "" && strings.Contains(spec, "#") {
		main, r, _ := strings.Cut(spec, "#")
		spec = main
		ref = r
	}

	// Handle local filesystem paths (e.g. in test fixtures or file remotes)
	if filepath.IsAbs(spec) {
		owner := filepath.Base(filepath.Dir(spec))
		repo := filepath.Base(spec)
		id := fmt.Sprintf("%s/%s", owner, repo)
		return ParsedSource{
			ID:       id,
			CloneURL: spec,
			Ref:      ref,
			Owner:    owner,
			Repo:     repo,
		}, nil
	}

	spec = strings.Trim(spec, "/")
	parts := strings.Split(spec, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ParsedSource{}, fmt.Errorf("invalid repository format %q; expected OWNER/REPO, github:OWNER/REPO, or URL", input)
	}

	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")

	if !githubIdentRE.MatchString(owner) || owner == "." || owner == ".." {
		return ParsedSource{}, fmt.Errorf("invalid repository owner %q", owner)
	}
	if !githubIdentRE.MatchString(repo) || repo == "." || repo == ".." {
		return ParsedSource{}, fmt.Errorf("invalid repository name %q", repo)
	}

	ref = strings.TrimSpace(ref)
	if ref != "" {
		if strings.HasPrefix(ref, "-") || strings.Contains(ref, "..") || strings.ContainsAny(ref, "\x00\n\r") {
			return ParsedSource{}, fmt.Errorf("invalid ref %q", ref)
		}
	}

	id := fmt.Sprintf("%s/%s", owner, repo)
	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)

	return ParsedSource{
		ID:       id,
		CloneURL: cloneURL,
		Ref:      ref,
		Owner:    owner,
		Repo:     repo,
	}, nil
}

// SourceCacheDir returns the persistent cache directory for a repository.
func SourceCacheDir(d dirs.Dirs, owner, repo string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(owner + "/" + repo)))
	suffix := hex.EncodeToString(sum[:])[:8]
	return filepath.Join(d.DotpackHome, "cache", "sources", owner, repo+"-"+suffix)
}

// FetchSource fetches a repository into its local cache and resolves the candidate commit SHA.
// It checks out the candidate commit in detached HEAD mode.
func FetchSource(ctx context.Context, ps ParsedSource, d dirs.Dirs) (commitSHA, cacheDir string, err error) {
	if d.DotpackHome == "" {
		return "", "", fmt.Errorf("DOTPACK_DOTPACK_HOME is unavailable")
	}

	cacheDir = SourceCacheDir(d, ps.Owner, ps.Repo)
	gitDir := filepath.Join(cacheDir, ".git")

	// Ensure parent dir exists
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir cache parent: %w", err)
	}

	// Check if already cloned
	if st, err := os.Stat(gitDir); err == nil && st.IsDir() {
		// Existing clone, fetch updates
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		_, _ = CurrentGitRunner(fetchCtx, cacheDir, "remote", "set-url", "origin", ps.CloneURL)
		if _, err := CurrentGitRunner(fetchCtx, cacheDir, "fetch", "--prune", "origin"); err != nil {
			return "", "", fmt.Errorf("fetch %s/%s: %w", ps.Owner, ps.Repo, err)
		}
		if ps.Ref == "" {
			// Update default branch symref if possible
			_, _ = CurrentGitRunner(fetchCtx, cacheDir, "remote", "set-head", "origin", "-a")
		} else {
			// If an explicit ref is requested, fetch it specifically in case it's a branch/tag/commit
			_, _ = CurrentGitRunner(fetchCtx, cacheDir, "fetch", "origin", ps.Ref)
		}
	} else {
		// Fresh clone
		_ = os.RemoveAll(cacheDir)
		tmpDir, err := os.MkdirTemp(filepath.Dir(cacheDir), ".clone-*")
		if err != nil {
			return "", "", fmt.Errorf("create temp clone dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		cloneCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		if _, err := CurrentGitRunner(cloneCtx, "", "clone", ps.CloneURL, tmpDir); err != nil {
			return "", "", fmt.Errorf("clone %s/%s: %w", ps.Owner, ps.Repo, err)
		}

		if ps.Ref != "" {
			_, _ = CurrentGitRunner(cloneCtx, tmpDir, "fetch", "origin", ps.Ref)
		}

		if err := os.Rename(tmpDir, cacheDir); err != nil {
			return "", "", fmt.Errorf("move clone to %s: %w", cacheDir, err)
		}
	}

	// Resolve commit SHA
	sha, err := resolveCommitSHA(ctx, cacheDir, ps.Ref)
	if err != nil {
		return "", "", fmt.Errorf("resolve ref %q for %s/%s: %w", ps.Ref, ps.Owner, ps.Repo, err)
	}

	// Checkout candidate commit detached and clean
	checkoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if _, err := CurrentGitRunner(checkoutCtx, cacheDir, "checkout", "--detach", sha); err != nil {
		return "", "", fmt.Errorf("checkout commit %s for %s/%s: %w", sha, ps.Owner, ps.Repo, err)
	}
	if _, err := CurrentGitRunner(checkoutCtx, cacheDir, "reset", "--hard", sha); err != nil {
		return "", "", fmt.Errorf("reset commit %s for %s/%s: %w", sha, ps.Owner, ps.Repo, err)
	}

	return sha, cacheDir, nil
}

func resolveCommitSHA(ctx context.Context, repoDir, ref string) (string, error) {
	parseCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if ref == "" {
		// Follow remote default branch
		// 1. Try refs/remotes/origin/HEAD
		if out, err := CurrentGitRunner(parseCtx, repoDir, "rev-parse", "refs/remotes/origin/HEAD^{commit}"); err == nil {
			sha := strings.TrimSpace(string(out))
			if commitSHARE.MatchString(sha) {
				return sha, nil
			}
		}
		// 2. Try origin/main
		if out, err := CurrentGitRunner(parseCtx, repoDir, "rev-parse", "refs/remotes/origin/main^{commit}"); err == nil {
			sha := strings.TrimSpace(string(out))
			if commitSHARE.MatchString(sha) {
				return sha, nil
			}
		}
		// 3. Try origin/master
		if out, err := CurrentGitRunner(parseCtx, repoDir, "rev-parse", "refs/remotes/origin/master^{commit}"); err == nil {
			sha := strings.TrimSpace(string(out))
			if commitSHARE.MatchString(sha) {
				return sha, nil
			}
		}
		// 4. Fall back to HEAD
		if out, err := CurrentGitRunner(parseCtx, repoDir, "rev-parse", "HEAD^{commit}"); err == nil {
			sha := strings.TrimSpace(string(out))
			if commitSHARE.MatchString(sha) {
				return sha, nil
			}
		}
		return "", fmt.Errorf("could not determine default branch commit")
	}

	// Explicit ref
	candidates := []string{
		"refs/remotes/origin/" + ref + "^{commit}",
		"refs/tags/" + ref + "^{commit}",
		"FETCH_HEAD^{commit}",
		ref + "^{commit}",
		ref,
	}

	for _, cand := range candidates {
		if out, err := CurrentGitRunner(parseCtx, repoDir, "rev-parse", cand); err == nil {
			sha := strings.TrimSpace(string(out))
			if commitSHARE.MatchString(sha) {
				return sha, nil
			}
		}
	}

	return "", fmt.Errorf("ref %q could not be resolved to a commit", ref)
}
