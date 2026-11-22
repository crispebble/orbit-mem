package memory

import (
	"slices"
	"testing"
)

// 지움 가드(index_guard_test.go와 같은 꼴): 기억 id를 담는 열을 가진 표가 새로 생기면 Forget이 그 행을 지울지
// 남길지 정해야 한다. 정하지 않은 표는 지운 기억의 흔적을 말없이 남기므로 여기서 먼저 깨진다.
func TestForgetGuard_기억_id_열을_가진_표는_지움이나_남김_목록에_있다(t *testing.T) {
	s, _ := newTestStore(t)
	rows, err := s.DB().Query(`SELECT m.name, p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p
		WHERE m.type='table' AND (p.name IN ('memory_id','other_id','memory_ids','old_id','new_id') OR (m.name='memory_links' AND p.name IN ('a','b')))
		ORDER BY m.name, p.name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	listed := func(table, column string) bool {
		return slices.ContainsFunc(forgetTables, func(c forgetTable) bool { return c.table == table && c.column == column }) ||
			slices.ContainsFunc(keepTables, func(c idColumn) bool { return c.table == table && c.column == column })
	}
	seen := 0
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		seen++
		if !listed(table, column) {
			t.Errorf("%s.%s holds memory ids but is in neither forgetTables nor keepTables", table, column)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen < 10 { // 훑기가 표를 못 찾으면 이 시험은 아무것도 지키지 않는다
		t.Fatalf("found only %d columns", seen)
	}
	// 목록의 표·열은 실제로 있어야 한다: 이름이 틀리면 지움 SQL이 깨지고 가드도 헛돈다.
	for _, c := range append(slices.Clone(keepTables), forgetColumns()...) {
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, c.table, c.column).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s.%s is listed but not in the schema (%v)", c.table, c.column, err)
		}
	}
}

func forgetColumns() []idColumn {
	out := make([]idColumn, len(forgetTables))
	for i, c := range forgetTables {
		out[i] = c.idColumn
	}
	return out
}
