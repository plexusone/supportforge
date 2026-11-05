package supportforge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/plexusone/supportforge/store"
)

// Defaults for clearing.
const (
	DefaultClearDelay = 7 * 24 * time.Hour
	DefaultMaxHold    = 90 * 24 * time.Hour
)

// clearBatch is how many cases Clear reads at a time.
const clearBatch = 100

// HoldChecker reports whether a data subject is under a legal hold. A held
// subject's cases are neither cleared nor erased.
type HoldChecker interface {
	Held(ctx context.Context, principalID string) (bool, error)
}

// ClearReport is what one run of [Service.Clear] did.
type ClearReport struct {
	Bodies    int      // bodies cleared
	Addresses int      // reply addresses cleared
	Held      []string // IDs of cases skipped because their requester is held
}

// Clear removes personal data that is no longer needed, from delivered
// cases only:
//
//   - a body, the clear delay after delivery;
//   - a reply address, the clear delay after the case's last activity
//     (delivery, the last reply or closure), while the desk does not report
//     the ticket open or pending;
//   - a reply address, after the maximum hold without activity, whatever
//     the desk reports.
//
// The title stays for the requester's list. Undelivered cases are never
// cleared, and a held requester's cases are skipped and reported.
//
// Clear is safe to run often and from several processes: each case is
// checked again as it is cleared, and a run after downtime catches up. Run
// it on a schedule, for example hourly. It returns what it did with every
// error it met; a case that failed is tried again on the next run.
func (s *Service) Clear(ctx context.Context) (ClearReport, error) {
	now := s.now()
	rule := store.ClearRule{
		Body:    now.Add(-s.clearDelay),
		ReplyTo: now.Add(-s.clearDelay),
		Ceiling: now.Add(-s.maxHold),
	}
	var report ClearReport
	var errs []error
	// Cleared cases leave the due set, so the offset counts only the cases
	// skipped so far: held, failed or no longer due.
	offset := 0
	for {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(errs, err)...)
		}
		cases, err := s.store.ClearDue(ctx, rule, store.Page{Limit: clearBatch, Offset: offset})
		if err != nil {
			return report, errors.Join(append(errs, fmt.Errorf("supportforge: finding cases to clear: %w", err))...)
		}
		for _, c := range cases {
			cleared, err := s.clearCase(ctx, c, rule, now, &report)
			if err != nil {
				errs = append(errs, fmt.Errorf("case %s: %w", c.ID, err))
			}
			if !cleared {
				offset++
			}
		}
		if len(cases) < clearBatch {
			return report, errors.Join(errs...)
		}
	}
}

// clearCase clears what is due of one case. It reports false when nothing
// was cleared, so the case is still in the due set.
func (s *Service) clearCase(ctx context.Context, c store.Case, rule store.ClearRule, now time.Time, report *ClearReport) (bool, error) {
	if s.holds != nil && c.Requester.PrincipalID != "" {
		held, err := s.holds.Held(ctx, c.Requester.PrincipalID)
		if err != nil {
			return false, fmt.Errorf("checking for a hold: %w", err)
		}
		if held {
			report.Held = append(report.Held, c.ID)
			return false, nil
		}
	}
	var body, address bool
	err := s.store.Update(ctx, c.ID, func(sc *store.Case) error {
		// Checked again under the update, so a reply or a reopening since
		// the case was listed keeps the address.
		body, address = rule.BodyDue(*sc), rule.ReplyToDue(*sc)
		if body {
			sc.Body, sc.BodyClearedAt = "", now
		}
		if address {
			sc.Requester.ReplyTo, sc.ReplyToClearedAt = "", now
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("clearing: %w", err)
	}
	if body {
		report.Bodies++
	}
	if address {
		report.Addresses++
	}
	return body || address, nil
}
