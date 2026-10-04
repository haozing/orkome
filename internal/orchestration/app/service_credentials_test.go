package app

import (
	"context"
	"testing"
	"time"

	"orkome/internal/contract/apierr"
)

type fakeCredentialRepo struct {
	rows      []Credential
	createErr error
}

func (f *fakeCredentialRepo) CreateCredential(_ context.Context, _, provider, _, _ string) (Credential, error) {
	if f.createErr != nil {
		return Credential{}, f.createErr
	}
	c := Credential{ID: "c-" + provider, Provider: provider, CreatedAt: time.Now()}
	f.rows = append(f.rows, c)
	return c, nil
}

func (f *fakeCredentialRepo) ListCredentials(_ context.Context, _ string) ([]Credential, error) {
	return f.rows, nil
}

func (f *fakeCredentialRepo) DeleteCredential(_ context.Context, _, id string) (bool, error) {
	for i, c := range f.rows {
		if c.ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func TestCredentialServiceCreateValidation(t *testing.T) {
	s := NewCredentialService(&fakeCredentialRepo{})
	cases := []struct {
		name, provider, secret, wantErr string
	}{
		{"provider empty", "", "sk-x", "invalid credential provider"},
		{"provider too long", string(make([]byte, 65)), "sk-x", "invalid credential provider"},
		{"secret empty", "openai", "", "invalid credential secret"},
		{"secret too long", "openai", string(make([]byte, 4097)), "invalid credential secret"},
		{"valid", " openai ", "sk-x", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := s.Create(context.Background(), "ws", tc.provider, tc.secret, "u")
			if tc.wantErr == "" {
				if err != nil || c.Provider != "openai" {
					t.Fatalf("want ok, got err=%v c=%+v", err, c)
				}
				return
			}
			if ae, ok := err.(*apierr.Error); !ok || ae.Message != tc.wantErr {
				t.Fatalf("want %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestCredentialServiceListEmptyNotNil(t *testing.T) {
	s := NewCredentialService(&fakeCredentialRepo{})
	list, err := s.List(context.Background(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Fatal("empty lists should render [] not null")
	}
}

func TestCredentialServiceDeleteNotFound(t *testing.T) {
	s := NewCredentialService(&fakeCredentialRepo{})
	err := s.Delete(context.Background(), "ws", "nope")
	if err == nil {
		t.Fatal("missing credential should be 404")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.NotFound {
		t.Fatalf("want E_NOT_FOUND, got %v", err)
	}
}
