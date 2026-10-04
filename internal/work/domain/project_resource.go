package domain

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	ResourceKindRepo     = "repo"
	ResourceKindLocalDir = "local_dir"
)

const resourceURIMaxLen = 2048

var winDriveRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func ValidateResourceKind(kind string) error {
	if kind != ResourceKindRepo && kind != ResourceKindLocalDir {
		return fmt.Errorf("kind must be %s or %s", ResourceKindRepo, ResourceKindLocalDir)
	}
	return nil
}

func ValidateResourceURI(kind, uri string) error {
	if uri == "" || len(uri) > resourceURIMaxLen {
		return fmt.Errorf("uri required and must be at most %d chars", resourceURIMaxLen)
	}
	switch kind {
	case ResourceKindRepo:
		if !strings.HasPrefix(uri, "https://") {
			return fmt.Errorf("repo uri must start with https://")
		}
	case ResourceKindLocalDir:
		if !winDriveRe.MatchString(uri) && !strings.HasPrefix(uri, "/") {
			return fmt.Errorf(`local_dir uri must be an absolute path (drive letter "X:\…" or leading "/")`)
		}
	default:
		return fmt.Errorf("kind must be %s or %s", ResourceKindRepo, ResourceKindLocalDir)
	}
	return nil
}
