package app

import (
	"fmt"

	"orkome/internal/work/domain"
)

func InvolvesCondition(start int) string {
	return fmt.Sprintf(`(wi.assignee_id = $%d::uuid OR wi.creator_id = $%d::uuid
	   OR EXISTS(SELECT 1 FROM work_item_subscriber wis
	             WHERE wis.work_item_id = wi.id AND wis.user_id = $%d::uuid))`, start, start, start)
}

func PropCondition(f domain.PropFilter, start int) (string, int) {
	k, v := start, start+1
	get := fmt.Sprintf("wi.properties->>$%d", k)
	switch f.Op {
	case domain.PropOpEmpty:
		return fmt.Sprintf("(%s IS NULL OR %s = '')", get, get), 1
	case domain.PropOpContains:
		return fmt.Sprintf("%s ILIKE '%%' || $%d || '%%'", get, v), 2
	}
	cmp := map[string]string{
		domain.PropOpEq: "=", domain.PropOpNe: "<>",
		domain.PropOpGt: ">", domain.PropOpLt: "<",
	}[f.Op]
	if f.Numeric {
		return fmt.Sprintf("(%s)::numeric %s $%d::numeric", get, cmp, v), 2
	}
	return fmt.Sprintf("%s %s $%d", get, cmp, v), 2
}
