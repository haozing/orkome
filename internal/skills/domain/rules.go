package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func ParseFrontmatter(content string) Frontmatter {
	var fm Frontmatter
	if !strings.HasPrefix(content, "---") {
		return fm
	}
	rest := content[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fm
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		if i := strings.Index(line, ":"); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			switch k {
			case "name":
				fm.Name = v
			case "description":
				fm.Description = v
			}
		}
	}
	return fm
}

const (
	MaxFilesPerSkill = 200
	MaxFileBytes     = 1 << 20
	MaxZipBytes      = 50 << 20
)

var binaryExtBlacklist = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".bin": true,
	".zip": true, ".tar": true, ".gz": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".pdf": true, ".woff": true, ".woff2": true, ".ttf": true,
}

func CleanPath(p string) (string, bool) {
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.Contains(p, "..") {
		return "", false
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	clean = strings.TrimPrefix(clean, "./")
	if strings.HasPrefix(clean, "/") || clean == "" || clean == "." {
		return "", false
	}
	return clean, true
}

func IsBinaryExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return binaryExtBlacklist[ext]
}

func MainFilenameGuard(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "skill.md"
}

var ErrManifestMismatch = errors.New("skill manifest mismatch: stored content tampered")

type ManifestFile struct {
	Path    string
	Content string
	SHA256  string
	Size    int
}

func ManifestDigest(files []ManifestFile) string {
	if len(files) == 0 {
		h := sha256.New()
		h.Write([]byte{0x00})
		return hex.EncodeToString(h.Sum(nil))
	}
	sorted := append([]ManifestFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	nodes := make([]string, len(sorted))
	for i, f := range sorted {
		h := sha256.New()
		h.Write([]byte{0x01})
		var lb [8]byte
		binary.BigEndian.PutUint64(lb[:], uint64(len(f.Path)))
		h.Write(lb[:])
		h.Write([]byte(f.Path))
		binary.BigEndian.PutUint64(lb[:], uint64(len(f.Content)))
		h.Write(lb[:])
		h.Write([]byte(f.Content))
		nodes[i] = hex.EncodeToString(h.Sum(nil))
	}
	for len(nodes) > 1 {
		var next []string
		for i := 0; i < len(nodes); i += 2 {
			r := i + 1
			if r >= len(nodes) {
				r = i
			}
			h := sha256.New()
			h.Write([]byte{0x02})
			h.Write([]byte(nodes[i]))
			h.Write([]byte(nodes[r]))
			next = append(next, hex.EncodeToString(h.Sum(nil)))
		}
		nodes = next
	}
	return nodes[0]
}

func VerifyManifest(files []ManifestFile) error {
	for _, f := range files {
		if f.Size != len(f.Content) {
			return ErrManifestMismatch
		}
		sum := sha256.Sum256([]byte(f.Content))
		if !strings.EqualFold(hex.EncodeToString(sum[:]), f.SHA256) {
			return ErrManifestMismatch
		}
	}
	return nil
}
