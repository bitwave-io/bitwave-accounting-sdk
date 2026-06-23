// Package expense implements bwx expense-report tagging, filtering, and
// rendering. Reports are emergent — entries tagged `expense-report:<id>` in
// their note are the report's contents; the report id is the tag value.
package expense

import "strings"

const (
	ExpenseReportKey = "expense-report"
	MerchantKey      = "merchant"
	ReimbursableKey  = "reimbursable"
)

// ComposeTags returns a slice of `key:value` tag tokens ready to be joined
// into an entry's note via the existing composeMemo path. Merchant strings
// are underscore-encoded to survive the space-delimited tag grammar — the
// reverse is handled by MerchantFromNote.
func ComposeTags(reportId, merchant string, reimbursable bool) []string {
	tags := []string{ExpenseReportKey + ":" + reportId}
	if merchant != "" {
		tags = append(tags, MerchantKey+":"+encodeValue(merchant))
	}
	if reimbursable {
		tags = append(tags, ReimbursableKey+":true")
	}
	return tags
}

// ReportIdFromNote returns the expense-report tag value from a note, or "" if
// no such tag exists. Strips a leading `;` and any free-form prose.
func ReportIdFromNote(note string) string {
	return tagValue(note, ExpenseReportKey)
}

// HasReport reports whether the note carries expense-report:<reportId>.
func HasReport(note, reportId string) bool {
	if reportId == "" {
		return false
	}
	return ReportIdFromNote(note) == reportId
}

// MerchantFromNote returns the merchant tag value (decoded from underscore
// encoding), or "".
func MerchantFromNote(note string) string {
	return decodeValue(tagValue(note, MerchantKey))
}

// ReimbursableFromNote reports whether the entry carries reimbursable:true.
func ReimbursableFromNote(note string) bool {
	return strings.EqualFold(tagValue(note, ReimbursableKey), "true")
}

// tagValue scans space-separated tokens for one shaped like key:value and
// returns the first match's value (empty when absent).
func tagValue(note, key string) string {
	for _, tok := range tokens(note) {
		k, v, ok := splitTag(tok)
		if ok && k == key {
			return v
		}
	}
	return ""
}

func tokens(note string) []string {
	s := strings.TrimSpace(note)
	s = strings.TrimPrefix(s, ";")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func splitTag(tok string) (key, value string, ok bool) {
	i := strings.IndexByte(tok, ':')
	if i <= 0 || i == len(tok)-1 {
		return "", "", false
	}
	return tok[:i], tok[i+1:], true
}

// Tag values can't contain spaces because notes are space-delimited. We
// encode user-supplied spaces as underscores on the way in and decode on the
// way out. A literal underscore is rare enough in merchant names that this
// trade-off is acceptable; if it becomes a problem we can switch to quoted
// values without changing the public API.
func encodeValue(s string) string {
	return strings.ReplaceAll(s, " ", "_")
}

func decodeValue(s string) string {
	return strings.ReplaceAll(s, "_", " ")
}
