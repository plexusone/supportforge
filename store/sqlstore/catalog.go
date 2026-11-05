package sqlstore

import "github.com/grokify/datacatalog"

// Table names, as the catalog and the database name them.
const (
	CasesTable  = "supportforge_cases"
	OutboxTable = "supportforge_outbox"
)

// Reasons for keeping a field when its data subject is erased.
const (
	retainNoPersonalData = "Describes the case or its handling; holds no personal data"
	retainIdentifier     = "Identifies the case; holds no personal data"
	retainLifecycle      = "Records when the case moved; holds no personal data"
)

// Catalog classifies every column of the store's tables in the
// [datacatalog] model, so a host can merge it with its own catalog to drive
// export, erasure and query guards. Field names are column names.
//
// The cases table is in privacy scope with principal_id as its subject key.
// A visitor's case has no subject key, and once its text and address are
// cleared it no longer identifies anyone. The outbox holds references only.
//
// Each call returns a new catalog, which the caller may change.
func Catalog() *datacatalog.Catalog {
	return &datacatalog.Catalog{
		Name:     "supportforge",
		Entities: []datacatalog.Entity{casesEntity(), outboxEntity()},
	}
}

func casesEntity() datacatalog.Entity {
	return datacatalog.Entity{
		Name:           CasesTable,
		Owner:          "supportforge",
		Description:    "Support requests and feedback, with their delivery and lifecycle",
		SubjectKey:     "principal_id",
		PrivacyScope:   true,
		HoldApplicable: true,
		Retention: "body is cleared a delay after delivery; reply_to once the conversation is quiet, " +
			"or after the maximum hold; rows are purged after the host's index retention",
		Fields: []datacatalog.Field{
			retained("id", datacatalog.TypeString, datacatalog.VisibilityInternal, retainIdentifier, true),
			retained("reference", datacatalog.TypeString, datacatalog.VisibilityInternal, retainIdentifier, true),
			retained("product", datacatalog.TypeString, datacatalog.VisibilityPublic, retainNoPersonalData, true),
			retained("kind", datacatalog.TypeString, datacatalog.VisibilityPublic, retainNoPersonalData, true),
			personal("title", "Kept for the requester's list; cleared on erasure"),
			personal("body", "Cleared a delay after delivery"),
			retained("page", datacatalog.TypeString, datacatalog.VisibilityInternal, retainNoPersonalData, true),
			retained("reply_wanted", datacatalog.TypeBool, datacatalog.VisibilityPublic, retainNoPersonalData, true),
			retained("shared", datacatalog.TypeBool, datacatalog.VisibilityPublic, retainNoPersonalData, true),
			{
				Name: "principal_id", Type: datacatalog.TypeString,
				Description: "The signed-in requester; empty for a visitor",
				Visibility:  datacatalog.VisibilityInternal,
				PII:         true, Erasure: datacatalog.ErasureTombstone, Export: true,
			},
			retained("organization_id", datacatalog.TypeString, datacatalog.VisibilityInternal, retainNoPersonalData, false),
			personal("reply_to", "Cleared once the conversation is quiet"),
			retained("created_at", datacatalog.TypeTime, datacatalog.VisibilityInternal, retainLifecycle, true),
			retained("share_reason", datacatalog.TypeString, datacatalog.VisibilityPublic, retainNoPersonalData, false),
			retained("status", datacatalog.TypeString, datacatalog.VisibilityPublic, retainNoPersonalData, true),
			retained("delivery_desk", datacatalog.TypeString, datacatalog.VisibilityInternal, retainNoPersonalData, false),
			retained("delivery_ticket_id", datacatalog.TypeString, datacatalog.VisibilityInternal, retainIdentifier, false),
			retained("desk_url", datacatalog.TypeString, datacatalog.VisibilityInternal, retainIdentifier, true),
			retained("delivered_at", datacatalog.TypeTime, datacatalog.VisibilityInternal, retainLifecycle, true),
			retained("last_reply_at", datacatalog.TypeTime, datacatalog.VisibilityInternal, retainLifecycle, false),
			retained("closed_at", datacatalog.TypeTime, datacatalog.VisibilityInternal, retainLifecycle, true),
			retained("status_checked_at", datacatalog.TypeTime, datacatalog.VisibilityInternal, retainLifecycle, false),
			retained("body_cleared_at", datacatalog.TypeTime, datacatalog.VisibilityInternal, retainLifecycle, false),
			retained("reply_to_cleared_at", datacatalog.TypeTime, datacatalog.VisibilityInternal, retainLifecycle, false),
		},
	}
}

func outboxEntity() datacatalog.Entity {
	internal := func(name string, t datacatalog.Type) datacatalog.Field {
		return datacatalog.Field{Name: name, Type: t, Visibility: datacatalog.VisibilityInternal}
	}
	return datacatalog.Entity{
		Name:        OutboxTable,
		Owner:       "supportforge",
		Description: "Cases waiting for delivery; references only",
		Retention:   "an entry is deleted when its case is delivered",
		Fields: []datacatalog.Field{
			internal("id", datacatalog.TypeString),
			internal("case_id", datacatalog.TypeString),
			internal("desk", datacatalog.TypeNumber),
			internal("attempts", datacatalog.TypeNumber),
			internal("next_attempt_at", datacatalog.TypeTime),
			internal("lease_until", datacatalog.TypeTime),
			internal("last_error", datacatalog.TypeString),
		},
	}
}

// personal is message text or an address: personal data, cleared on erasure
// and included in an export.
func personal(name, description string) datacatalog.Field {
	return datacatalog.Field{
		Name: name, Type: datacatalog.TypeString, Description: description,
		Visibility: datacatalog.VisibilityPublic,
		PII:        true, Erasure: datacatalog.ErasureClear, Export: true,
	}
}

// retained is kept when its data subject is erased.
func retained(name string, t datacatalog.Type, v datacatalog.Visibility, reason string, export bool) datacatalog.Field {
	return datacatalog.Field{
		Name: name, Type: t, Visibility: v,
		Erasure: datacatalog.ErasureRetain, RetainReason: reason, Export: export,
	}
}
