package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OutboxEntry is a case waiting for delivery. It holds no personal data.
type OutboxEntry struct {
	ent.Schema
}

// Annotations of the OutboxEntry.
func (OutboxEntry) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "supportforge_outbox"}}
}

// Fields of the OutboxEntry.
func (OutboxEntry) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").NotEmpty().Immutable(),
		field.String("case_id").NotEmpty().Immutable(),
		field.Int("desk").Comment("Index of the desk being tried, in the configured order"),
		field.Int("attempts").Comment("Attempts at that desk"),
		field.Time("next_attempt_at"),
		field.Time("lease_until").Optional().Nillable().Comment("Claimed by a worker until then"),
		field.String("last_error").Comment("The class of the last failure, never its text"),
	}
}

// Edges of the OutboxEntry.
func (OutboxEntry) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("support_case", SupportCase.Type).
			Ref("entries").
			Field("case_id").
			Unique().
			Required().
			Immutable(),
	}
}

// Indexes of the OutboxEntry.
func (OutboxEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("next_attempt_at"),
	}
}
