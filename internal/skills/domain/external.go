package domain

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	SourceGitHub = "github"

	SourceSkillsSh = "skills_sh"

	SourceBareFile = "file"
)

const MaxURLFetchBytes = MaxZipBytes

type SourceParts struct {
	Kind  string
	Owner string
	Repo  string
	Skill string
}

func DetectSource(rawURL string) (SourceParts, error) {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return SourceParts{}, fmt.Errorf("empty url")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return SourceParts{}, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return SourceParts{}, fmt.Errorf("unsupported scheme %q (want https)", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")

	var path []string
	for _, seg := range segs {
		if seg != "" {
			path = append(path, seg)
		}
	}
	switch host {
	case "github.com", "www.github.com":
		if len(path) < 2 {
			return SourceParts{}, fmt.Errorf("github url must be github.com/{owner}/{repo}")
		}
		return SourceParts{Kind: SourceGitHub, Owner: path[0], Repo: strings.TrimSuffix(path[1], ".git")}, nil
	case "skills.sh", "www.skills.sh":
		if len(path) != 3 {
			return SourceParts{}, fmt.Errorf("skills.sh url must be skills.sh/{owner}/{repo}/{skill-name}")
		}
		return SourceParts{Kind: SourceSkillsSh, Owner: path[0], Repo: path[1], Skill: path[2]}, nil
	default:
		if len(path) == 0 {
			return SourceParts{}, fmt.Errorf("bare url has no fetchable path")
		}
		return SourceParts{Kind: SourceBareFile}, nil
	}
}

func (p SourceParts) GitHubTarballURL() string {
	return "https://codeload.github.com/" + url.PathEscape(p.Owner) + "/" + url.PathEscape(p.Repo) + "/tar.gz/HEAD"
}
