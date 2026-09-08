package sourceregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/internal/validator"
)

// DiscoveredSkill represents a validated skill package discovered within a repository.
type DiscoveredSkill struct {
	Name          string
	Skill         *resource.Skill
	SkillDir      string
	SourceRelPath string
	ContentSHA256 string
	RawSKILLMD    []byte
}

// DiscoverSkills scans a repository root (with optional discoveryPath override) for all skill packages.
func DiscoverSkills(repoRoot, discoveryPath string) ([]DiscoveredSkill, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return nil, fmt.Errorf("repository root is required")
	}
	absRepo, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root %s: %w", repoRoot, err)
	}

	startDir := absRepo
	if strings.TrimSpace(discoveryPath) != "" {
		if filepath.IsAbs(discoveryPath) {
			startDir = filepath.Clean(discoveryPath)
		} else {
			startDir = filepath.Join(absRepo, discoveryPath)
		}
	}

	if st, err := os.Stat(startDir); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("discovery path %s does not exist", startDir)
	} else if err != nil {
		return nil, fmt.Errorf("stat discovery path %s: %w", startDir, err)
	} else if !st.IsDir() {
		return nil, fmt.Errorf("discovery path %s is not a directory", startDir)
	}

	var discovered []DiscoveredSkill
	seenNames := make(map[string]string)

	walkErr := filepath.WalkDir(startDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip VCS and dependency directories
		if entry.IsDir() {
			base := entry.Name()
			if base == ".git" || base == "node_modules" || base == ".cache" || base == ".dotpack" || base == ".github" {
				return filepath.SkipDir
			}

			// Check if this directory is a skill root containing SKILL.md
			skillFilePath := filepath.Join(path, "SKILL.md")
			fi, err := os.Lstat(skillFilePath)
			if err == nil {
				if fi.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("skill %s is a symlink; symlinks are not supported", skillFilePath)
				}
				if !fi.Mode().IsRegular() {
					return fmt.Errorf("skill %s is not a regular file", skillFilePath)
				}

				// This directory is a Skill Package!
				disc, discErr := loadDiscoveredSkill(absRepo, path, skillFilePath)
				if discErr != nil {
					return discErr
				}

				relPath, _ := filepath.Rel(absRepo, path)
				relPathSlash := filepath.ToSlash(relPath)
				if priorPath, ok := seenNames[disc.Name]; ok {
					return fmt.Errorf("duplicate skill name %q at %s and %s", disc.Name, priorPath, relPathSlash)
				}
				seenNames[disc.Name] = relPathSlash
				discovered = append(discovered, disc)

				// Stop package-root discovery beneath this skill package
				return filepath.SkipDir
			}
		}

		return nil
	})

	if walkErr != nil {
		return nil, walkErr
	}

	sort.Slice(discovered, func(i, j int) bool {
		return discovered[i].Name < discovered[j].Name
	})

	return discovered, nil
}

func loadDiscoveredSkill(repoRoot, skillDir, skillFilePath string) (DiscoveredSkill, error) {
	raw, err := os.ReadFile(skillFilePath)
	if err != nil {
		return DiscoveredSkill{}, fmt.Errorf("read %s: %w", skillFilePath, err)
	}

	skill, err := resource.ParseSkill(raw)
	if err != nil {
		return DiscoveredSkill{}, fmt.Errorf("parse %s: %w", skillFilePath, err)
	}

	supportFiles, err := loadSkillSupportFiles(skillDir, skillFilePath)
	if err != nil {
		return DiscoveredSkill{}, err
	}
	skill.SupportFiles = supportFiles

	if errs := validator.ValidateSkill(skill); len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		return DiscoveredSkill{}, fmt.Errorf("validation error for skill at %s: %s", skillDir, strings.Join(msgs, "; "))
	}

	rel, err := filepath.Rel(repoRoot, skillDir)
	if err != nil {
		return DiscoveredSkill{}, fmt.Errorf("relative path for %s: %w", skillDir, err)
	}
	relSlash := filepath.ToSlash(rel)

	contentSHA := computeSkillContentSHA(raw, supportFiles)

	return DiscoveredSkill{
		Name:          skill.Name,
		Skill:         skill,
		SkillDir:      skillDir,
		SourceRelPath: relSlash,
		ContentSHA256: contentSHA,
		RawSKILLMD:    raw,
	}, nil
}

func loadSkillSupportFiles(skillDir, skillFilePath string) ([]resource.SupportFile, error) {
	var supportFiles []resource.SupportFile
	err := filepath.WalkDir(skillDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == skillDir {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if path == skillFilePath {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill support file %s is a symlink; symlinks are not supported", path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat skill support file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill support file %s is not a regular file", path)
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read skill support file %s: %w", path, err)
		}
		supportFiles = append(supportFiles, resource.SupportFile{
			RelPath: filepath.ToSlash(rel),
			Content: raw,
			Mode:    info.Mode().Perm(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(supportFiles, func(i, j int) bool {
		return supportFiles[i].RelPath < supportFiles[j].RelPath
	})
	return supportFiles, nil
}

func computeSkillContentSHA(skillMD []byte, supportFiles []resource.SupportFile) string {
	h := sha256.New()
	h.Write([]byte("SKILL.md\x00"))
	h.Write(skillMD)
	h.Write([]byte("\x00"))
	for _, sf := range supportFiles {
		h.Write([]byte(sf.RelPath))
		h.Write([]byte("\x00"))
		h.Write(sf.Content)
		h.Write([]byte("\x00"))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
