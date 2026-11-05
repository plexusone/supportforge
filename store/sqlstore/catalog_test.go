package sqlstore_test

import (
	"fmt"
	"slices"
	"testing"

	"entgo.io/ent/dialect/sql/schema"
	"entgo.io/ent/schema/field"
	"github.com/grokify/datacatalog"

	"github.com/plexusone/supportforge/store/sqlstore"
	"github.com/plexusone/supportforge/store/sqlstore/internal/ent/migrate"
)

func TestCatalogIsValid(t *testing.T) {
	if err := sqlstore.Catalog().Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestCatalogClassifiesEveryColumn fails when a column has no
// classification, or a classification names no column, so the schema and
// the catalog cannot drift apart.
func TestCatalogClassifiesEveryColumn(t *testing.T) {
	for _, p := range mismatches(sqlstore.Catalog(), migrate.Tables) {
		t.Error(p)
	}
}

func TestMismatchesFindsAnUnclassifiedColumn(t *testing.T) {
	table := &schema.Table{Name: sqlstore.OutboxTable, Columns: []*schema.Column{
		{Name: "id", Type: field.TypeString},
		{Name: "case_id", Type: field.TypeString},
		{Name: "desk", Type: field.TypeInt},
		{Name: "attempts", Type: field.TypeInt},
		{Name: "next_attempt_at", Type: field.TypeTime},
		{Name: "lease_until", Type: field.TypeTime},
		{Name: "last_error", Type: field.TypeBool}, // the wrong type
		{Name: "requester_email", Type: field.TypeString},
	}}
	cases := &schema.Table{Name: sqlstore.CasesTable}
	got := mismatches(sqlstore.Catalog(), []*schema.Table{table, cases})
	want := []string{
		"supportforge_outbox.last_error: classified as string, stored as bool",
		"supportforge_outbox.requester_email: not classified",
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("mismatches = %q, missing %q", got, w)
		}
	}
	if !slices.Contains(got, "supportforge_cases.title: classified but not stored") {
		t.Errorf("mismatches = %q, missing a stale classification", got)
	}
}

// mismatches compares a catalog with the tables Ent creates.
func mismatches(cat *datacatalog.Catalog, tables []*schema.Table) []string {
	var out []string
	stored := map[string]bool{}
	for _, table := range tables {
		stored[table.Name] = true
		entity, ok := findEntity(cat, table.Name)
		if !ok {
			out = append(out, table.Name+": table not classified")
			continue
		}
		columns := map[string]bool{}
		for _, col := range table.Columns {
			columns[col.Name] = true
			f, ok := findField(entity, col.Name)
			switch {
			case !ok:
				out = append(out, fmt.Sprintf("%s.%s: not classified", table.Name, col.Name))
			case f.Type != catalogType(col.Type):
				out = append(out, fmt.Sprintf("%s.%s: classified as %s, stored as %s", table.Name, col.Name, f.Type, col.Type))
			}
		}
		for _, f := range entity.Fields {
			if !columns[f.Name] {
				out = append(out, fmt.Sprintf("%s.%s: classified but not stored", table.Name, f.Name))
			}
		}
	}
	for _, e := range cat.Entities {
		if !stored[e.Name] {
			out = append(out, e.Name+": classified but not stored")
		}
	}
	return out
}

func findEntity(cat *datacatalog.Catalog, name string) (datacatalog.Entity, bool) {
	i := slices.IndexFunc(cat.Entities, func(e datacatalog.Entity) bool { return e.Name == name })
	if i < 0 {
		return datacatalog.Entity{}, false
	}
	return cat.Entities[i], true
}

func findField(e datacatalog.Entity, name string) (datacatalog.Field, bool) {
	i := slices.IndexFunc(e.Fields, func(f datacatalog.Field) bool { return f.Name == name })
	if i < 0 {
		return datacatalog.Field{}, false
	}
	return e.Fields[i], true
}

// catalogType maps an Ent column type to the catalog's coarse type. A type
// the store does not use yet maps to none, which never matches.
func catalogType(t field.Type) datacatalog.Type {
	switch t {
	case field.TypeString:
		return datacatalog.TypeString
	case field.TypeBool:
		return datacatalog.TypeBool
	case field.TypeTime:
		return datacatalog.TypeTime
	case field.TypeInt, field.TypeInt64:
		return datacatalog.TypeNumber
	default:
		return ""
	}
}
