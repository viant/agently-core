package invariant

import "testing"

type orphanFixture struct {
	Id           string  `sqlx:"id"`
	ParentId     *string `sqlx:"parent_id"`
	OtherId      *string `sqlx:"other_id"`
	Status       string  `sqlx:"status"`
	ShouldDelete bool    `sqlx:"-"`
	Has          *orphanFixtureHas
}
type orphanFixtureHas struct{ Id, ParentId, OtherId, Status, ShouldDelete bool }

func TestOrphanDetachShapeProtectsOtherFieldsAndIdentity(t *testing.T) {
	parent := "missing"
	previous := &orphanFixture{Id: "row", ParentId: &parent, Status: "ready"}
	for _, tc := range []struct {
		name      string
		entity    *orphanFixture
		column    string
		wantError bool
	}{
		{name: "one explicit nil reference", entity: &orphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true, ParentId: true}}, column: "parent_id"},
		{name: "replacement reference rejected", entity: &orphanFixture{Id: "row", ParentId: &parent, Has: &orphanFixtureHas{Id: true, ParentId: true}}, column: "parent_id", wantError: true},
		{name: "status mutation rejected", entity: &orphanFixture{Id: "row", Status: "failed", Has: &orphanFixtureHas{Id: true, ParentId: true, Status: true}}, column: "parent_id", wantError: true},
		{name: "second nil rejected", entity: &orphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true, ParentId: true, OtherId: true}}, column: "parent_id", wantError: true},
		{name: "unmarked nil rejected", entity: &orphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true}}, column: "parent_id", wantError: true},
		{name: "identity change rejected", entity: &orphanFixture{Id: "different", Has: &orphanFixtureHas{Id: true, ParentId: true}}, column: "parent_id", wantError: true},
		{name: "delete flag rejected", entity: &orphanFixture{Id: "row", ShouldDelete: true, Has: &orphanFixtureHas{Id: true, ParentId: true}}, column: "parent_id", wantError: true},
		{name: "unknown column rejected", entity: &orphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true, ParentId: true}}, column: "arbitrary", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOrphanDetach(tc.entity, previous, tc.column, []string{"parent_id", "other_id"}, "Id")
			if (err != nil) != tc.wantError {
				t.Fatalf("validation=%v wantError=%v", err, tc.wantError)
			}
		})
	}
}

type quotedOrphanFixture struct {
	Id           string  `sqlx:"id"`
	ParentId     *string "sqlx:\"parent_id|`parent_id`\""
	OtherId      *string "sqlx:\"other_id|`other_id`\""
	Status       string  `sqlx:"status"`
	ShouldDelete bool    `sqlx:"-"`
	Has          *orphanFixtureHas
}

func TestOrphanDetachCanonicalSQLxAliasPreservesShapeGuards(t *testing.T) {
	parent := "missing"
	previous := &quotedOrphanFixture{Id: "row", ParentId: &parent, Status: "ready"}
	for _, tc := range []struct {
		name      string
		entity    *quotedOrphanFixture
		column    string
		wantError bool
	}{
		{"quoted SQL spelling", &quotedOrphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true, ParentId: true}}, "parent_id", false},
		{"replacement reference", &quotedOrphanFixture{Id: "row", ParentId: &parent, Has: &orphanFixtureHas{Id: true, ParentId: true}}, "parent_id", true},
		{"identity changed", &quotedOrphanFixture{Id: "foreign", Has: &orphanFixtureHas{Id: true, ParentId: true}}, "parent_id", true},
		{"identity unmarked", &quotedOrphanFixture{Id: "row", Has: &orphanFixtureHas{ParentId: true}}, "parent_id", true},
		{"nil unmarked", &quotedOrphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true}}, "parent_id", true},
		{"second reference", &quotedOrphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true, ParentId: true, OtherId: true}}, "parent_id", true},
		{"status changed", &quotedOrphanFixture{Id: "row", Status: "failed", Has: &orphanFixtureHas{Id: true, ParentId: true, Status: true}}, "parent_id", true},
		{"delete flagged", &quotedOrphanFixture{Id: "row", ShouldDelete: true, Has: &orphanFixtureHas{Id: true, ParentId: true}}, "parent_id", true},
		{"SQL spelling is not authorized column", &quotedOrphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true, ParentId: true}}, "`parent_id`", true},
		{"whole alias is not authorized column", &quotedOrphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true, ParentId: true}}, "parent_id|`parent_id`", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOrphanDetach(tc.entity, previous, tc.column, []string{"parent_id", "other_id"}, "Id")
			if (err != nil) != tc.wantError {
				t.Fatalf("validation=%v wantError=%v", err, tc.wantError)
			}
		})
	}
}

type wrongLogicalOrphanFixture struct {
	Id       string  `sqlx:"id"`
	ParentId *string "sqlx:\"other_id|`parent_id`\""
	Has      *orphanFixtureHas
}

func TestOrphanDetachSQLSpellingCannotReplaceLogicalColumn(t *testing.T) {
	parent := "missing"
	current := &wrongLogicalOrphanFixture{Id: "row", Has: &orphanFixtureHas{Id: true, ParentId: true}}
	previous := &wrongLogicalOrphanFixture{Id: "row", ParentId: &parent}
	if err := ValidateOrphanDetach(current, previous, "parent_id", []string{"parent_id"}, "Id"); err == nil {
		t.Fatal("alternate SQL spelling authorized another logical column")
	}
}
