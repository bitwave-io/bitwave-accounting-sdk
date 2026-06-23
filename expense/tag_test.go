package expense

import (
	"strings"
	"testing"
)

func TestComposeTags_RequiredReport(t *testing.T) {
	tags := ComposeTags("Q1-travel", "", false)
	if len(tags) != 1 || tags[0] != "expense-report:Q1-travel" {
		t.Fatalf("expected sole expense-report tag, got %v", tags)
	}
}

func TestComposeTags_FullSet(t *testing.T) {
	tags := ComposeTags("Q1-travel", "Acme Corp", true)
	got := strings.Join(tags, " ")
	for _, want := range []string{"expense-report:Q1-travel", "merchant:Acme_Corp", "reimbursable:true"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestComposeTags_NoReimbursableWhenFalse(t *testing.T) {
	tags := ComposeTags("Q1", "Acme", false)
	for _, tg := range tags {
		if strings.HasPrefix(tg, "reimbursable:") {
			t.Fatalf("did not expect reimbursable tag, got %v", tags)
		}
	}
}

func TestReportIdFromNote(t *testing.T) {
	cases := []struct {
		note string
		want string
	}{
		{"; expense-report:Q1-travel merchant:Acme", "Q1-travel"},
		{"merchant:Acme expense-report:Q2", "Q2"},
		{"no tags here", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := ReportIdFromNote(tc.note)
		if got != tc.want {
			t.Errorf("ReportIdFromNote(%q) = %q, want %q", tc.note, got, tc.want)
		}
	}
}

func TestHasReport(t *testing.T) {
	if !HasReport("expense-report:Q1 merchant:x", "Q1") {
		t.Error("Q1 should match")
	}
	if HasReport("expense-report:Q2", "Q1") {
		t.Error("Q2 should not match Q1")
	}
	if HasReport("", "Q1") {
		t.Error("empty note should not match")
	}
}

func TestReimbursableFromNote(t *testing.T) {
	if !ReimbursableFromNote("expense-report:Q1 reimbursable:true") {
		t.Error("reimbursable:true should be detected")
	}
	if ReimbursableFromNote("expense-report:Q1 reimbursable:false") {
		t.Error("reimbursable:false should be false")
	}
	if ReimbursableFromNote("expense-report:Q1") {
		t.Error("absent reimbursable tag should be false")
	}
}

func TestMerchantFromNote(t *testing.T) {
	if got := MerchantFromNote("expense-report:Q1 merchant:Acme_Corp"); got != "Acme Corp" {
		t.Errorf("MerchantFromNote: want 'Acme Corp', got %q", got)
	}
	if got := MerchantFromNote("no tag"); got != "" {
		t.Errorf("MerchantFromNote empty: got %q", got)
	}
}
