package domain

import (
	"fmt"
	"testing"
)

func TestRRFFuse(t *testing.T) {

	title := []SearchHit{
		{WorkItemID: "a", Title: "A", Source: "title"},
		{WorkItemID: "b", Title: "B", Source: "title"},
	}
	body := []SearchHit{
		{WorkItemID: "b", Title: "B", Source: "body"},
		{WorkItemID: "c", Title: "C", Source: "body"},
	}
	comment := []SearchHit{
		{WorkItemID: "a", Title: "A", Source: "comment"},
		{WorkItemID: "d", Title: "D", Source: "comment"},
	}

	got := RRFFuse([][]SearchHit{title, body, comment}, 10)

	if len(got) != 4 {
		t.Fatalf("want 4 hits, got %d", len(got))
	}
	if got[0].WorkItemID != "a" {
		t.Errorf("first place should be a (dual path, both rank 1), got %s", got[0].WorkItemID)
	}
	if got[1].WorkItemID != "b" {
		t.Errorf("second place should be b (dual path), got %s", got[1].WorkItemID)
	}

	if got[2].WorkItemID != "c" || got[3].WorkItemID != "d" {
		t.Errorf("stable order c,d on ties; got %s,%s", got[2].WorkItemID, got[3].WorkItemID)
	}

	got = RRFFuse([][]SearchHit{title, body, comment}, 2)
	if len(got) != 2 || got[0].WorkItemID != "a" || got[1].WorkItemID != "b" {
		t.Errorf("top=2 should truncate to [a,b], got %+v", got)
	}

	if got = RRFFuse(nil, 10); len(got) != 0 {
		t.Errorf("empty input should give empty output, got %+v", got)
	}
}

func TestRRFFuseMany(t *testing.T) {
	var path []SearchHit
	for i := 0; i < 100; i++ {
		path = append(path, SearchHit{WorkItemID: fmt.Sprintf("id%03d", i)})
	}
	got := RRFFuse([][]SearchHit{path}, 30)
	if len(got) != 30 {
		t.Fatalf("want top 30, got %d", len(got))
	}
	if got[0].WorkItemID != "id000" || got[29].WorkItemID != "id029" {
		t.Errorf("single-path fusion should keep order, got %s..%s", got[0].WorkItemID, got[29].WorkItemID)
	}
}
