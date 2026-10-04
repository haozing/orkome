package domain

type SearchHit struct {
	WorkItemID string
	Title      string
	Snippet    string
	Source     string
}

func RRFFuse(paths [][]SearchHit, top int) []SearchHit {
	if top <= 0 {
		top = 30
	}
	type scored struct {
		hit   SearchHit
		score float64
	}
	byID := map[string]*scored{}
	var order []string
	for _, path := range paths {
		for rank, h := range path {
			inc := 1.0 / float64(rank+60)
			if s, ok := byID[h.WorkItemID]; ok {
				s.score += inc
			} else {
				byID[h.WorkItemID] = &scored{hit: h, score: inc}
				order = append(order, h.WorkItemID)
			}
		}
	}

	idx := map[string]int{}
	for i, id := range order {
		idx[id] = i
	}
	sorted := make([]string, len(order))
	copy(sorted, order)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0; j-- {
			a, b := sorted[j-1], sorted[j]
			if byID[a].score < byID[b].score || (byID[a].score == byID[b].score && idx[a] > idx[b]) {
				sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
			} else {
				break
			}
		}
	}
	if len(sorted) > top {
		sorted = sorted[:top]
	}
	out := make([]SearchHit, len(sorted))
	for i, id := range sorted {
		out[i] = byID[id].hit
	}
	return out
}
