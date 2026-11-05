// Package schema defines the tables of supportforge's SQL store.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SupportCase is a support case as stored. Kind, status and share reason are
// strings, not enums, so that adding a value is a code change only.
//
// Times that may be unset are nullable: NULL is the zero time.
type SupportCase struct {
	ent.Schema
}

// Annotations of the SupportCase.
func (SupportCase) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "supportforge_cases"}}
}

// Fields of the SupportCase.
func (SupportCase) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("reference").NotEmpty().Immutable().Unique(),
		field.String("product"),
		field.String("kind"),
		field.String("title").Comment("Personal data: kept for the requester's list, cleared on erasure"),
		field.Text("body").Comment("Personal data: cleared after delivery"),
		field.String("page"),
		field.Bool("reply_wanted"),
		field.Bool("shared"),
		field.String("principal_id").Comment("Empty for a visitor"),
		field.String("organization_id").Comment("Empty for a visitor"),
		field.String("reply_to").Comment("Personal data: cleared after the case goes quiet"),
		field.Time("created_at"),
		field.String("share_reason"),
		field.String("status"),
		field.String("delivery_desk"),
		field.String("delivery_ticket_id"),
		field.String("desk_url"),
		field.Time("delivered_at").Optional().Nillable(),
		field.Time("last_reply_at").Optional().Nillable(),
		field.Time("closed_at").Optional().Nillable(),
		field.Time("status_checked_at").Optional().Nillable(),
		field.Time("body_cleared_at").Optional().Nillable(),
		field.Time("reply_to_cleared_at").Optional().Nillable(),
	}
}

// Edges of the SupportCase.
func (SupportCase) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("entries", OutboxEntry.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Indexes of the SupportCase.
func (SupportCase) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("principal_id", "created_at"),
		index.Fields("delivery_desk", "status", "status_checked_at"),
		index.Fields("delivered_at"), // the clear job
	}
}
