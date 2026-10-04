package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateResourceKind(t *testing.T) {
	assert.NoError(t, ValidateResourceKind("repo"))
	assert.NoError(t, ValidateResourceKind("local_dir"))
	for _, k := range []string{"", "git", "ssh", "Repo", "local-dir"} {
		assert.Error(t, ValidateResourceKind(k), "kind %q should be rejected", k)
	}
}

func TestValidateResourceURI(t *testing.T) {

	assert.NoError(t, ValidateResourceURI("repo", "https://github.com/org/repo.git"))
	for _, u := range []string{
		"http://github.com/org/repo.git",
		"git@github.com:org/repo.git",
		"github.com/org/repo",
		"",
	} {
		assert.Error(t, ValidateResourceURI("repo", u), "repo uri %q should be rejected", u)
	}

	for _, u := range []string{
		`D:\code\miniCrew`,
		`D:/code/miniCrew`,
		`d:\workspace`,
		`/home/user/project`,
		`/tmp/build`,
	} {
		assert.NoError(t, ValidateResourceURI("local_dir", u), "local_dir uri %q should pass", u)
	}
	for _, u := range []string{
		`code\relative`,
		`code/relative`,
		`D:relative`,
		`\\server\share`,
		`./local`,
	} {
		assert.Error(t, ValidateResourceURI("local_dir", u), "local_dir uri %q should be rejected", u)
	}

	assert.Error(t, ValidateResourceURI("bogus", "https://x.example/repo"))

	assert.Error(t, ValidateResourceURI("repo", "https://"+string(make([]byte, 2100))))
}
