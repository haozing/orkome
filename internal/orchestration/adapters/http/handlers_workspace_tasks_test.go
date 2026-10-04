package http

import (
	"errors"
	"testing"

	"orkome/internal/contract/apierr"
)

func TestParseTaskStatusFilter(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
		wantErr  bool
	}{
		{"empty = full set", "", nil, false},
		{"single value", "failed", []string{"failed"}, false},
		{"multi-value dedup", "failed,failed,completed", []string{"failed", "completed"}, false},
		{"active syntax sugar", "active", []string{"queued", "dispatched", "running", "awaiting_input"}, false},
		{"active mixed with explicit values dedup", "running,active", []string{"running", "queued", "dispatched", "awaiting_input"}, false},
		{"all does not filter", "all", nil, false},
		{"awaiting_input valid", "awaiting_input", []string{"awaiting_input"}, false},
		{"active+all mixed", "active,all", []string{"queued", "dispatched", "running", "awaiting_input"}, false},
		{"empty segments tolerated", "failed,,", []string{"failed"}, false},
		{"unknown status", "paused", nil, true},
		{"case-sensitive (invalid)", "Failed", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTaskStatusFilter(tc.in)
			if tc.wantErr {
				var ae *apierr.Error
				if !errors.As(err, &ae) || ae.Code != apierr.Validation {
					t.Fatalf("want E_VALIDATION, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v want %v", got, tc.want)
				}
			}
		})
	}
}
