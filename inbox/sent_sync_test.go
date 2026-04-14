package inbox

import (
	"sort"
	"testing"

	"github.com/dominicgisler/imap-spam-cleaner/checkpoint"
	"github.com/dominicgisler/imap-spam-cleaner/imap"
)

func TestExtractRecipientEmails_IncludesToCcBcc(t *testing.T) {
	m := imap.Message{
		To:  "Alice <alice@example.com>, bob@example.com",
		Cc:  "Carol <carol@example.com>",
		Bcc: "dave@example.com",
	}
	got := extractRecipientEmails(m)
	want := []string{"alice@example.com", "bob@example.com", "carol@example.com", "dave@example.com"}

	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("unexpected number of recipients: got=%d want=%d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mismatch at index %d: got=%s want=%s; got=%v", i, got[i], want[i], got)
		}
	}
}

func TestShouldReseedSentWhitelist(t *testing.T) {
	tests := []struct {
		name         string
		cp           *checkpoint.Checkpoint
		contactCount int
		want         bool
	}{
		{name: "nil checkpoint", cp: nil, contactCount: 0, want: false},
		{name: "no checkpoint progress", cp: &checkpoint.Checkpoint{LastUID: 0}, contactCount: 0, want: false},
		{name: "checkpoint advanced but contacts present", cp: &checkpoint.Checkpoint{LastUID: 123}, contactCount: 2, want: false},
		{name: "checkpoint advanced and contacts empty", cp: &checkpoint.Checkpoint{LastUID: 123}, contactCount: 0, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldReseedSentWhitelist(tt.cp, tt.contactCount)
			if got != tt.want {
				t.Fatalf("shouldReseedSentWhitelist() = %v, want %v", got, tt.want)
			}
		})
	}
}
