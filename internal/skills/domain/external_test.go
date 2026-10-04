package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectSource(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
		kind    string
		owner   string
		repo    string
		skill   string
	}{
		{name: "GitHub repo", url: "https://github.com/octo/skill-repo", kind: SourceGitHub, owner: "octo", repo: "skill-repo"},
		{name: "GitHub with .git suffix", url: "https://github.com/octo/skill-repo.git", kind: SourceGitHub, owner: "octo", repo: "skill-repo"},
		{name: "GitHub www", url: "https://www.github.com/octo/repo", kind: SourceGitHub, owner: "octo", repo: "repo"},
		{name: "GitHub without scheme gets https", url: "github.com/octo/repo", kind: SourceGitHub, owner: "octo", repo: "repo"},
		{name: "GitHub missing repo rejected", url: "https://github.com/octo", wantErr: true},
		{name: "skills.sh three segments", url: "https://skills.sh/octo/mono/pdf-skill", kind: SourceSkillsSh, owner: "octo", repo: "mono", skill: "pdf-skill"},
		{name: "skills.sh two segments rejected", url: "https://skills.sh/octo/mono", wantErr: true},
		{name: "skills.sh four segments rejected", url: "https://skills.sh/a/b/c/d", wantErr: true},
		{name: "bare file direct link", url: "https://example.com/skills/pdf/SKILL.md", kind: SourceBareFile},
		{name: "bare file raw.githubusercontent", url: "https://raw.githubusercontent.com/octo/repo/main/SKILL.md", kind: SourceBareFile},
		{name: "bare host without path rejected", url: "https://example.com", wantErr: true},
		{name: "empty URL rejected", url: "", wantErr: true},
		{name: "blank URL rejected", url: "", wantErr: true},
		{name: "non-http scheme rejected", url: "ftp://example.com/x.md", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := DetectSource(tc.url)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.kind, p.Kind)
			assert.Equal(t, tc.owner, p.Owner)
			assert.Equal(t, tc.repo, p.Repo)
			assert.Equal(t, tc.skill, p.Skill)
		})
	}
}

func TestGitHubTarballURL(t *testing.T) {
	p := SourceParts{Kind: SourceGitHub, Owner: "octo", Repo: "skill-repo"}
	assert.Equal(t, "https://codeload.github.com/octo/skill-repo/tar.gz/HEAD", p.GitHubTarballURL())

	bad := SourceParts{Owner: "a/b", Repo: "c"}
	assert.Equal(t, "https://codeload.github.com/a%2Fb/c/tar.gz/HEAD", bad.GitHubTarballURL())
}
